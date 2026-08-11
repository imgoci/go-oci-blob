package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// resumeReader keeps a blob download alive across broken streams. On
// a mid-stream failure it reestablishes the transfer with a ranged
// GET from the last delivered byte, so the verifying reader above it
// never sees the gap and no byte is hashed twice.
type resumeReader struct {
	// ctx bounds every reestablishment request.
	ctx context.Context
	// client executes the ranged GETs.
	client *Client
	// target is the blob URL the download came from.
	target *url.URL
	// body is the current network stream.
	body io.ReadCloser
	// offset counts bytes already delivered to the reader above.
	offset int64
	// stalls counts consecutive reestablishments without progress.
	stalls int
}

// Read delivers bytes from the stream, transparently reconnecting
// from the current offset when the stream breaks mid-body. End of
// stream, context cancellation, and exhausted reconnect budgets
// surface unchanged.
func (r *resumeReader) Read(p []byte) (int, error) {
	for {
		n, err := r.body.Read(p)
		r.offset += int64(n)
		if n > 0 {
			r.stalls = 0
		}
		switch {
		case err == nil, errors.Is(err, io.EOF):
			return n, err
		case r.ctx.Err() != nil:
			// The caller gave up; the break is not the registry's.
			return n, err
		}

		r.stalls++
		if r.stalls >= r.client.retry.attempts() {
			return n, fmt.Errorf("blob stream kept breaking at byte %d: %w", r.offset, err)
		}
		if rerr := r.reestablish(err); rerr != nil {
			return n, rerr
		}
		if n > 0 {
			return n, nil
		}
	}
}

// Close closes the current network stream.
func (r *resumeReader) Close() error {
	return r.body.Close()
}

// reestablish replaces the broken stream with a ranged GET from the
// current offset. A 206 continues exactly where the break happened; a
// 200 means the registry ignored the range, so the already-delivered
// prefix is discarded from the fresh stream instead. cause is the
// read error that triggered the reconnect.
func (r *resumeReader) reestablish(cause error) error {
	if err := sleepContext(r.ctx, r.client.retry.backoffDelay(r.stalls, 0)); err != nil {
		return fmt.Errorf("blob stream broke at byte %d: %w", r.offset, cause)
	}

	resp, err := r.client.doRetry(r.ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.target.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("building resume request: %w", err)
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", r.offset))
		return req, nil
	})
	if err != nil {
		return fmt.Errorf("resuming blob download at byte %d (cause: %w): %w", r.offset, cause, err)
	}

	switch {
	case resp.StatusCode == http.StatusPartialContent:
	case isSuccess(resp.StatusCode):
		if _, err := io.CopyN(io.Discard, resp.Body, r.offset); err != nil {
			_ = resp.Body.Close()
			return fmt.Errorf("resuming blob download at byte %d (cause: %w): "+
				"registry ignored the range and the replayed stream ended early: %w",
				r.offset, cause, err)
		}
	default:
		defer resp.Body.Close()
		return fmt.Errorf("resuming blob download at byte %d (cause: %w): %w",
			r.offset, cause, interpretError(resp))
	}

	_ = r.body.Close()
	r.body = resp.Body
	return nil
}

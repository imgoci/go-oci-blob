package blob

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sync"

	"github.com/opencontainers/go-digest"
)

// Pull downloads a blob and returns a reader over its bytes.
//
// The reader verifies content as it flows: when the stream ends, the
// final Read returns [io.EOF] only if the bytes hashed to dgst, and
// [ErrDigestMismatch] otherwise. Nothing is buffered; the blob
// streams straight from the registry (or from the blob storage the
// registry redirects to). Close the reader when done. A missing blob
// is an error matching [ErrNotFound].
//
// A stream that breaks mid-body resumes under the client's
// [RetryPolicy] with a ranged request from the last delivered byte;
// digest verification carries across the resume, so no byte is
// hashed twice. With [WithParallelPull] the blob arrives via
// concurrent ranged fetches instead, emitted in order through the
// same verifying reader, falling back to a single stream when the
// registry does not serve ranges.
//
// Example:
//
//	rc, err := client.Pull(ctx, repo, dgst)
//	if err != nil {
//		return err
//	}
//	defer rc.Close()
//	if _, err := io.Copy(dst, rc); err != nil {
//		return err // includes ErrDigestMismatch on corrupt content
//	}
func (c *Client) Pull(
	ctx context.Context, repo Repository, dgst digest.Digest, opts ...TransferOption,
) (io.ReadCloser, error) {
	if err := validateTarget(repo, dgst); err != nil {
		return nil, err
	}
	cfg := applyTransferOptions(opts)
	tracker := newProgressTracker(cfg.progress, -1)

	target := blobURL(c.scheme(), repo, dgst)
	if c.pullWorkers > 0 {
		stream, err := c.parallelPull(ctx, target, tracker)
		if err != nil {
			return nil, fmt.Errorf("pulling blob %s from %s/%s: %w", dgst, repo.Host, repo.Name, err)
		}
		return newVerifyReader(stream, dgst), nil
	}

	resp, err := c.get(ctx, target, "") //nolint:bodyclose // verifying reader owns the body
	if err != nil {
		return nil, fmt.Errorf("pulling blob %s from %s/%s: %w", dgst, repo.Host, repo.Name, err)
	}
	tracker.setTotal(resp.ContentLength)
	resume := newResumeReader(ctx, c, target, resp.Body)
	return newVerifyReader(progressify(resume, tracker), dgst), nil
}

// PullRange downloads length bytes of a blob starting at offset.
//
// The returned reader is deliberately unverified: the digest covers
// the whole blob, so a partial body cannot be checked against it.
// Callers that need integrity on partial reads build it above the
// library. When the registry ignores the Range header and answers
// with the whole blob, PullRange discards the leading offset bytes
// and caps the reader at length, so the reader serves the requested
// window either way. Shorter valid portions are supported for up to
// 16 successful partial responses; further fragmentation stops with
// an error instead of issuing unbounded requests. A window that starts
// at or past the end of the blob is an error.
func (c *Client) PullRange(
	ctx context.Context,
	repo Repository,
	dgst digest.Digest,
	offset, length int64,
	opts ...TransferOption,
) (io.ReadCloser, error) {
	if err := validateTarget(repo, dgst); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, fmt.Errorf("negative offset %d", offset)
	}
	if length <= 0 {
		return nil, fmt.Errorf("non-positive length %d", length)
	}
	if length-1 > math.MaxInt64-offset {
		return nil, fmt.Errorf("range offset %d and length %d overflow", offset, length)
	}
	cfg := applyTransferOptions(opts)
	tracker := newProgressTracker(cfg.progress, length)

	end := offset + length - 1
	rangeHeader := fmt.Sprintf("bytes=%d-%d", offset, end)
	target := blobURL(c.scheme(), repo, dgst)
	resp, err := c.get(ctx, target, rangeHeader)
	if err != nil {
		return nil, fmt.Errorf("pulling range %s of blob %s from %s/%s: %w",
			rangeHeader, dgst, repo.Host, repo.Name, err)
	}
	if resp.StatusCode == http.StatusPartialContent {
		parsed, err := validateContentRange(resp.Header.Get("Content-Range"), offset, end)
		if err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("pulling range %s of blob %s from %s/%s: %w",
				rangeHeader, dgst, repo.Host, repo.Name, err)
		}
		stream := newRangeReader(ctx, c, target, resp.Body, parsed, end)
		return progressify(stream, tracker), nil
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("pulling range %s of blob %s from %s/%s: registry returned unexpected status %d",
			rangeHeader, dgst, repo.Host, repo.Name, resp.StatusCode)
	}

	// The registry ignored the Range header and sent the whole blob;
	// carve the requested window out of the full stream.
	buffered := bufio.NewReader(resp.Body)
	if offset > 0 {
		if _, err := io.CopyN(io.Discard, buffered, offset); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("blob %s is shorter than range offset %d: %w",
				dgst, offset, contextOperationError(ctx, err))
		}
	}
	if _, err := buffered.Peek(1); err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("blob %s has no data at range offset %d: %w",
			dgst, offset, contextOperationError(ctx, err))
	}
	if err := ctx.Err(); err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("reading blob %s at range offset %d: %w", dgst, offset, err)
	}
	stream := newRangeReader(ctx, c, target, resp.Body, contentRange{
		start: offset,
		end:   end,
		total: -1,
	}, end)
	stream.reader = buffered
	return progressify(stream, tracker), nil
}

// get issues a GET for u under the retry policy. A full request
// accepts only 200; a ranged request accepts 206 or a 200 fallback
// from a registry that ignored Range. Every other status is
// interpreted as an error and its response is closed.
func (c *Client) get(ctx context.Context, u *url.URL, rangeHeader string) (*http.Response, error) {
	return c.getAfterAttempts(ctx, u, rangeHeader, 0)
}

// getAfterAttempts issues a GET using only the request attempts left after
// spent. A successful body records the lifetime total for later resume reads.
func (c *Client) getAfterAttempts(
	ctx context.Context, u *url.URL, rangeHeader string, spent int,
) (*http.Response, error) {
	remaining := c.retry.attempts() - spent
	if remaining <= 0 {
		return nil, fmt.Errorf("request budget exhausted after %d attempts", spent)
	}
	retryClient := *c
	retryClient.retry.MaxAttempts = remaining
	attempts := spent
	resp, err := retryClient.doRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		attempts++
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	validStatus := resp.StatusCode == http.StatusOK
	if rangeHeader != "" {
		validStatus = validStatus || resp.StatusCode == http.StatusPartialContent
	}
	if !validStatus {
		defer resp.Body.Close()
		return nil, interpretError(resp)
	}
	resp.Body = &requestAttemptBody{
		ReadCloser: resp.Body,
		attempts:   attempts,
	}
	return resp, nil
}

// requestAttemptBody carries the number of request attempts already spent
// before a response body became readable. A resumable stream uses it to keep
// every later request inside the same retry budget.
type requestAttemptBody struct {
	// ReadCloser is the successful response body.
	io.ReadCloser

	// attempts is the number of request attempts spent obtaining the body.
	attempts int
}

// requestAttempts returns the request attempts spent obtaining the body.
func (b *requestAttemptBody) requestAttempts() int {
	return b.attempts
}

// validateContentRange checks that a partial response starts at the
// requested byte and does not extend past the requested window.
func validateContentRange(header string, start, end int64) (contentRange, error) {
	parsed, ok := parseContentRange(header)
	if !ok {
		return contentRange{}, fmt.Errorf("registry returned invalid Content-Range %q", header)
	}
	if parsed.start != start {
		return contentRange{}, fmt.Errorf(
			"registry returned Content-Range %q starting at %d instead of requested byte %d",
			header, parsed.start, start)
	}
	if parsed.end > end {
		return contentRange{}, fmt.Errorf(
			"registry returned Content-Range %q beyond requested end byte %d", header, end)
	}
	return parsed, nil
}

// rangeReader delivers exactly one requested byte window. When a
// compliant 206 carries only part of the request, it fetches the
// remainder with another ranged request.
type rangeReader struct {
	// readMu serializes calls to Read while Close remains concurrent.
	readMu sync.Mutex
	// mu protects the mutable stream state shared with Close.
	mu sync.Mutex
	// ctx bounds follow-up range requests.
	ctx context.Context
	// cancel stops follow-up requests when the reader closes.
	cancel context.CancelFunc
	// client executes follow-up range requests.
	client *Client
	// target is the blob URL being read.
	target *url.URL
	// body is the response body currently owned by the reader.
	body io.ReadCloser
	// reader is the view read from body, potentially buffered.
	reader io.Reader
	// remaining is the number of bytes left in the current response.
	remaining int64
	// next is the expected start of the next partial response.
	next int64
	// end is the inclusive final byte requested by the caller.
	end int64
	// total is the representation size learned from Content-Range, or
	// -1 when the registry ignored Range.
	total int64
	// parts counts successfully validated partial responses. It is zero
	// when the registry ignored the Range header.
	parts int
	// closed prevents reads and response installation after Close.
	closed bool
}

// newRangeReader takes ownership of body for the validated partial
// response parsed and requested final byte end.
func newRangeReader(
	ctx context.Context,
	client *Client,
	target *url.URL,
	body io.ReadCloser,
	parsed contentRange,
	end int64,
) *rangeReader {
	readCtx, cancel := context.WithCancel(ctx)
	next := int64(0)
	parts := 0
	if parsed.total >= 0 {
		next = parsed.end + 1
		parts = 1
	}
	return &rangeReader{
		ctx:       readCtx,
		cancel:    cancel,
		client:    client,
		target:    target,
		body:      body,
		reader:    body,
		remaining: parsed.end - parsed.start + 1,
		next:      next,
		end:       end,
		total:     parsed.total,
		parts:     parts,
	}
}

// Read returns only bytes inside the requested window and fetches a
// follow-up partial response when the server supplied a shorter one.
func (r *rangeReader) Read(p []byte) (int, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()

	if len(p) == 0 {
		if err := r.stateError(); err != nil {
			return 0, err
		}
		return 0, nil
	}

	for {
		if err := r.advance(); err != nil {
			return 0, err
		}
		n, drained, err := r.readCurrent(p)
		if n > 0 || err != nil || !drained {
			return n, err
		}
	}
}

// readCurrent reads from the active response without crossing its
// advertised Content-Range. drained asks Read to advance immediately.
func (r *rangeReader) readCurrent(p []byte) (int, bool, error) {
	r.mu.Lock()
	reader := r.reader
	remaining := r.remaining
	r.mu.Unlock()
	if int64(len(p)) > remaining {
		p = p[:int(remaining)]
	}

	n, err := reader.Read(p)
	r.mu.Lock()
	r.remaining -= int64(n)
	remaining = r.remaining
	r.mu.Unlock()

	if stateErr := r.stateError(); stateErr != nil {
		return n, false, stateErr
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, false, err
	}
	if errors.Is(err, io.EOF) && remaining > 0 {
		return n, false, fmt.Errorf("range response ended with %d bytes still expected: %w",
			remaining, io.ErrUnexpectedEOF)
	}
	if n > 0 {
		return n, false, nil
	}
	return 0, remaining == 0, nil
}

// Close cancels follow-up requests and closes the current response
// body. It is safe to call concurrently with Read.
func (r *rangeReader) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	body := r.body
	r.body = nil
	r.reader = nil
	r.mu.Unlock()

	r.cancel()
	if body != nil {
		return body.Close()
	}
	return nil
}

// advance closes a fully consumed response and opens the next
// partial response when the requested window is not complete.
func (r *rangeReader) advance() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return io.ErrClosedPipe
	}
	if err := r.ctx.Err(); err != nil {
		r.mu.Unlock()
		return err
	}
	if r.remaining > 0 {
		r.mu.Unlock()
		return nil
	}
	body := r.body
	r.body = nil
	r.reader = nil
	next, end, total, parts := r.next, r.end, r.total, r.parts
	r.mu.Unlock()

	if body != nil {
		_ = body.Close()
	}
	if total < 0 {
		return io.EOF
	}
	if next > end {
		return io.EOF
	}
	if total >= 0 && next >= total {
		return fmt.Errorf("blob ended at byte %d before requested end byte %d: %w",
			total, end, io.ErrUnexpectedEOF)
	}
	if parts == maxRangeParts {
		return fmt.Errorf(
			"registry split the requested range across more than %d successful partial responses",
			maxRangeParts)
	}

	rangeHeader := fmt.Sprintf("bytes=%d-%d", next, end)
	resp, err := r.client.get(r.ctx, r.target, rangeHeader)
	if err != nil {
		if stateErr := r.stateError(); stateErr != nil {
			return stateErr
		}
		return fmt.Errorf("fetching remainder %s: %w", rangeHeader, err)
	}
	if resp.StatusCode != http.StatusPartialContent {
		_ = resp.Body.Close()
		return fmt.Errorf("fetching remainder %s: registry stopped honoring range requests (status %d)",
			rangeHeader, resp.StatusCode)
	}
	parsed, err := validateContentRange(resp.Header.Get("Content-Range"), next, end)
	if err != nil {
		_ = resp.Body.Close()
		return fmt.Errorf("fetching remainder %s: %w", rangeHeader, err)
	}
	if parsed.total != total {
		_ = resp.Body.Close()
		return fmt.Errorf("fetching remainder %s: blob size changed from %d to %d",
			rangeHeader, total, parsed.total)
	}

	return r.installRangeContinuation(resp.Body, parsed, parts)
}

// installRangeContinuation adopts a validated partial response unless Close
// won the race while the follow-up request was in flight.
func (r *rangeReader) installRangeContinuation(
	body io.ReadCloser, parsed contentRange, parts int,
) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = body.Close()
		return io.ErrClosedPipe
	}
	r.body = body
	r.reader = body
	r.remaining = parsed.end - parsed.start + 1
	r.next = parsed.end + 1
	r.parts = parts + 1
	r.mu.Unlock()
	return nil
}

// stateError reports closure or parent context completion without
// replacing those conditions with a stale transport error.
func (r *rangeReader) stateError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return io.ErrClosedPipe
	}
	return r.ctx.Err()
}

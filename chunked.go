package blob

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/opencontainers/go-digest"
)

// chunked.go implements the WithChunkedUpload path: PATCH per chunk
// with Content-Range, the registry's Range acknowledgement verified
// after every chunk, and an empty PUT to commit. Hosted registries
// are known to accept chunks and silently drop them, so the ack
// check is the point of this file: an ack that does not advance
// abandons the upload instead of trusting the registry.

// chunkedOnce runs one chunked upload attempt. The bool carries the
// same restartability meaning as pushOnce's; an acknowledgement that
// does not advance is deliberately not restartable, because a
// registry that drops chunks does so deterministically.
func (c *Client) chunkedOnce(
	ctx context.Context, repo Repository, dgst digest.Digest, size int64, r io.Reader,
	tracker *progressTracker,
) (bool, error) {
	session, retryable, err := c.openSession(ctx, repo)
	if err != nil {
		return retryable, err
	}

	// The registry's stated minimum wins over a smaller configured
	// chunk (OCI-Chunk-Min-Length).
	chunk := max(c.chunkSize, session.minChunk)
	for offset := int64(0); offset < size; {
		n := min(chunk, size-offset)
		if retryable, err := c.patchChunk(ctx, session, r, offset, n); err != nil {
			return retryable, err
		}
		offset += n
		// The ack verified this chunk, so its bytes are committed.
		tracker.set(offset)
	}
	return c.commitUpload(ctx, session.url, dgst, 0, http.NoBody)
}

// patchChunk uploads the chunk [offset, offset+n) to the session,
// verifies the registry acknowledged exactly the bytes sent so far,
// and moves the session to the Location the response names.
func (c *Client) patchChunk(
	ctx context.Context, session *uploadSession, r io.Reader, offset, n int64,
) (bool, error) {
	end := offset + n - 1
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPatch, session.url.String(), io.LimitReader(r, n))
	if err != nil {
		return false, fmt.Errorf("building chunk request: %w", err)
	}
	req.ContentLength = n
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Range", fmt.Sprintf("%d-%d", offset, end))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return true, fmt.Errorf("uploading chunk %d-%d: %w", offset, end, err)
	}
	defer resp.Body.Close()
	if !isSuccess(resp.StatusCode) {
		regErr := interpretError(resp)
		return retryableRegistryStatus(regErr), fmt.Errorf("uploading chunk %d-%d: %w", offset, end, regErr)
	}

	if ack, ok := parseRangeAck(resp.Header.Get("Range")); !ok || ack != end {
		return false, fmt.Errorf(
			"registry acknowledged Range %q after chunk %d-%d: the session did not advance by the "+
				"bytes just sent, so the registry is dropping chunks (a known failure of hosted "+
				"registries' chunked upload); abandoning the upload",
			resp.Header.Get("Range"), offset, end)
	}

	if location := resp.Header.Get("Location"); location != "" {
		next, err := resolveLocation(session.url, location)
		if err != nil {
			return false, fmt.Errorf("after chunk %d-%d: %w", offset, end, err)
		}
		session.url = next
	}
	return false, nil
}

// parseRangeAck parses an upload session's Range acknowledgement
// ("0-<end>", tolerating a bytes= prefix) and returns the inclusive
// end of the acknowledged range.
func parseRangeAck(header string) (int64, bool) {
	header = strings.TrimPrefix(header, "bytes=")
	start, endText, found := strings.Cut(header, "-")
	if !found || start != "0" {
		return 0, false
	}
	end, err := strconv.ParseInt(endText, 10, 64)
	if err != nil || end < 0 {
		return 0, false
	}
	return end, true
}

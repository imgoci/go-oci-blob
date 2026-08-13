package blob

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	tracker *progressTracker, replay *readerReplay, wire *wireProgressTracker,
) (bool, error) {
	session, retryable, err := c.openSession(ctx, repo, dgst.Algorithm())
	if err != nil {
		return retryable, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = c.cancelUploadSession(ctx, session)
		}
	}()

	// The registry's stated minimum wins over a smaller configured
	// chunk (OCI-Chunk-Min-Length).
	chunk := max(c.chunkSize, session.minChunk)
	for offset := int64(0); offset < size; {
		n := min(chunk, size-offset)
		finalChunk := offset+n == size
		chunkRetryable, chunkErr := c.patchChunk(ctx, session, r, offset, n, finalChunk, replay, wire)
		if chunkErr != nil {
			return chunkRetryable, chunkErr
		}
		offset += n
		// The ack verified this chunk advanced the upload session.
		tracker.set(offset)
	}
	retryable, err = c.commitUpload(ctx, session, dgst, 0, http.NoBody, nil, wire)
	if err != nil {
		return retryable, err
	}
	committed = true
	return false, nil
}

// patchChunk uploads the chunk [offset, offset+n) to the session,
// verifies the registry acknowledged exactly the bytes sent so far,
// and moves the session to the Location the response names.
func (c *Client) patchChunk(
	ctx context.Context, session *uploadSession, r io.Reader, offset, n int64,
	final bool, replay *readerReplay, wire *wireProgressTracker,
) (bool, error) {
	end := offset + n - 1
	req, initialBody, err := newPatchRequest(ctx, session, r, offset, n, final, replay, wire)
	if err != nil {
		return false, err
	}

	resp, err := c.doLocationRequest(req, session.registry)
	if err != nil {
		return patchRequestError(err, initialBody, replay, offset, end)
	}
	if !c.writeRedirects && replayRedirect(resp.StatusCode) {
		drainAndClose(resp.Body)
		body := currentUploadBody(initialBody, replay)
		body.waitReleased()
		if sourceErr := body.sourceErrorIfReleased(); sourceErr != nil {
			return false, fmt.Errorf("uploading chunk %d-%d: %w", offset, end, sourceErr)
		}
		return false, fmt.Errorf("uploading chunk %d-%d: %w", offset, end, errWriteRedirectRejected)
	}
	if replay == nil && replayRedirect(resp.StatusCode) {
		_, locationErr := resolveLocation(
			responseRequestURL(resp, session.url), resp.Header.Get("Location"))
		drainAndClose(resp.Body)
		body := currentUploadBody(initialBody, replay)
		body.waitReleased()
		if sourceErr := body.sourceErrorIfReleased(); sourceErr != nil {
			return false, fmt.Errorf("uploading chunk %d-%d: %w", offset, end, sourceErr)
		}
		if locationErr != nil {
			return false, fmt.Errorf(
				"uploading chunk %d-%d: %w: %w", offset, end, errInvalidRedirectTarget, locationErr)
		}
		return false, fmt.Errorf(
			"uploading chunk %d-%d: registry returned %d but the reader is not an io.Seeker, "+
				"so the request body cannot be replayed",
			offset, end, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusAccepted {
		return rejectPatchResponse(resp, initialBody, replay, offset, end)
	}

	ack, validAck := parseRangeAck(resp.Header.Get("Range"))
	location := resp.Header.Get("Location")
	var next *url.URL
	var locationErr error
	if location != "" {
		next, locationErr = resolveLocation(responseRequestURL(resp, session.url), location)
	}
	drainAndClose(resp.Body)
	body := currentUploadBody(initialBody, replay)
	body.waitReleased()
	if err := body.validate(); err != nil {
		return false, fmt.Errorf("uploading chunk %d-%d: %w", offset, end, err)
	}

	if !validAck || ack != end {
		return false, fmt.Errorf(
			"registry acknowledged Range %q after chunk %d-%d: the session did not advance by the "+
				"bytes just sent, so the registry is dropping chunks (a known failure of hosted "+
				"registries' chunked upload); abandoning the upload",
			resp.Header.Get("Range"), offset, end)
	}

	if location != "" {
		if locationErr != nil {
			return false, fmt.Errorf("after chunk %d-%d: %w", offset, end, locationErr)
		}
		session.url = next
	}
	return false, nil
}

// newPatchRequest builds one exact-length chunk request and its redirect
// replay hook.
func newPatchRequest(
	ctx context.Context,
	session *uploadSession,
	r io.Reader,
	offset, n int64,
	final bool,
	replay *readerReplay,
	wire *wireProgressTracker,
) (*http.Request, *uploadBody, error) {
	end := offset + n - 1
	var start int64
	if replay != nil {
		var err error
		start, err = replay.position()
		if err != nil {
			return nil, nil, fmt.Errorf(
				"capturing chunk %d-%d for redirect replay: %w", offset, end, err)
		}
	}
	exact := newExactSizeReader(r, n, final)
	exact.offset = offset
	body := newUploadBody(exact, wire)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, session.url.String(), body)
	if err != nil {
		return nil, nil, fmt.Errorf("building chunk request: %w", err)
	}
	if replay != nil {
		if err := replay.register(body); err != nil {
			return nil, nil, fmt.Errorf("registering chunk %d-%d request body: %w", offset, end, err)
		}
		req.GetBody = replay.getBody(start, offset, n, final, wire)
	}
	req.ContentLength = n
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Range", fmt.Sprintf("%d-%d", offset, end))
	return req, body, nil
}

// patchRequestError keeps a proven source failure ahead of a secondary
// transport error and classifies method-changing redirects as nonretryable.
func patchRequestError(
	err error,
	initialBody *uploadBody,
	replay *readerReplay,
	offset, end int64,
) (bool, error) {
	body := currentUploadBody(initialBody, replay)
	body.closeAndWait()
	if sourceErr := body.sourceErrorIfReleased(); sourceErr != nil {
		return false, fmt.Errorf("uploading chunk %d-%d: %w", offset, end, sourceErr)
	}
	return retryableRequestError(err),
		fmt.Errorf("uploading chunk %d-%d: %w", offset, end, err)
}

// rejectPatchResponse interprets a non-202 response before inspecting source
// consumption, preserving the registry's retry signal.
func rejectPatchResponse(
	resp *http.Response,
	initialBody *uploadBody,
	replay *readerReplay,
	offset, end int64,
) (bool, error) {
	regErr := interpretError(resp)
	_ = resp.Body.Close()
	body := currentUploadBody(initialBody, replay)
	body.waitReleased()
	if sourceErr := body.sourceErrorIfReleased(); sourceErr != nil {
		return false, fmt.Errorf("uploading chunk %d-%d: %w", offset, end, sourceErr)
	}
	return retryableRegistryStatus(regErr), fmt.Errorf("uploading chunk %d-%d: %w", offset, end, regErr)
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

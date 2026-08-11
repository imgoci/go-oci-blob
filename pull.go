package blob

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

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
	resume := &resumeReader{ctx: ctx, client: c, target: target, body: resp.Body}
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
// window either way. A window that starts at or past the end of the
// blob is an error.
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
	cfg := applyTransferOptions(opts)
	tracker := newProgressTracker(cfg.progress, length)

	rangeHeader := fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	resp, err := c.get(ctx, blobURL(c.scheme(), repo, dgst), rangeHeader)
	if err != nil {
		return nil, fmt.Errorf("pulling range %s of blob %s from %s/%s: %w",
			rangeHeader, dgst, repo.Host, repo.Name, err)
	}
	if resp.StatusCode == http.StatusPartialContent {
		return progressify(resp.Body, tracker), nil
	}

	// The registry ignored the Range header and sent the whole blob;
	// carve the requested window out of the full stream.
	if offset > 0 {
		if _, err := io.CopyN(io.Discard, resp.Body, offset); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("blob %s is shorter than range offset %d: %w", dgst, offset, err)
		}
	}
	window := &boundedBody{window: io.LimitReader(resp.Body, length), body: resp.Body}
	return progressify(window, tracker), nil
}

// get issues a GET for u under the retry policy and returns the
// response only when the status sits in the 2xx family. On any other
// status it interprets the error body, closes the response, and
// returns the error. A non-empty rangeHeader is sent as the Range
// header.
func (c *Client) get(ctx context.Context, u *url.URL, rangeHeader string) (*http.Response, error) {
	resp, err := c.doRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	if !isSuccess(resp.StatusCode) {
		defer resp.Body.Close()
		return nil, interpretError(resp)
	}
	return resp, nil
}

// boundedBody serves a fixed window of a response body while keeping
// the original body's Close, releasing the connection when the caller
// is done with the window.
type boundedBody struct {
	// window is the length-capped view over the body.
	window io.Reader
	// body is the underlying response body to close.
	body io.ReadCloser
}

// Read reads from the capped window.
func (b *boundedBody) Read(p []byte) (int, error) {
	return b.window.Read(p)
}

// Close closes the underlying response body.
func (b *boundedBody) Close() error {
	return b.body.Close()
}

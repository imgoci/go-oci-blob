package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/opencontainers/go-digest"
)

// Push uploads a blob monolithically: one POST to open an upload
// session, one PUT to send the bytes and commit them under dgst.
// With [WithChunkedUpload] the bytes travel in verified PATCH chunks
// instead; see that option for why chunked stays opt-in.
//
// The size is mandatory and must match the number of bytes r yields;
// registries need it as Content-Length, and there is no unknown-length
// upload. A caller that does not know the size spools the data first
// and comes back with a number. The registry rejects the commit when
// the content does not hash to dgst, so a corrupt stream cannot be
// stored silently.
//
// A failed upload restarts from byte zero under the client's
// [RetryPolicy]. Restarting needs the reader's bytes again, so r must
// implement [io.Seeker] (as [bytes.Reader], [strings.Reader], and
// [os.File] do); with a non-seekable reader the upload fails on the
// first transient error instead of retrying.
//
// Example:
//
//	dgst := digest.FromBytes(data)
//	err := client.Push(ctx, repo, dgst, int64(len(data)), bytes.NewReader(data))
func (c *Client) Push(
	ctx context.Context,
	repo Repository,
	dgst digest.Digest,
	size int64,
	r io.Reader,
	opts ...TransferOption,
) error {
	if err := validateTarget(repo, dgst); err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("size %d is invalid: pushes require the exact blob size", size)
	}
	if r == nil {
		return errors.New("nil reader")
	}
	applyTransferOptions(opts)

	uploadOnce := c.pushOnce
	if c.chunkSize > 0 {
		uploadOnce = c.chunkedOnce
	}

	rewind := rewinder(r)
	attempts := c.retry.attempts()
	for attempt := 1; ; attempt++ {
		retryable, err := uploadOnce(ctx, repo, dgst, size, r)
		if err == nil {
			return nil
		}
		err = fmt.Errorf("pushing blob %s to %s/%s: %w", dgst, repo.Host, repo.Name, err)
		if !retryable || ctx.Err() != nil || attempt == attempts {
			return err
		}
		if rewind == nil {
			return fmt.Errorf(
				"%w (upload restart needs the bytes again, but the reader is not an io.Seeker)", err)
		}
		if rerr := rewind(); rerr != nil {
			return fmt.Errorf("rewinding reader to restart upload: %w (restart cause: %w)", rerr, err)
		}
		if serr := sleepContext(ctx, c.retry.backoffDelay(attempt, retryAfterOf(err))); serr != nil {
			return err
		}
	}
}

// pushOnce runs one POST+PUT monolithic upload attempt. The bool
// reports whether the failure is worth restarting the upload for:
// transport-level breaks and retryable statuses are, everything else
// is not.
func (c *Client) pushOnce(
	ctx context.Context, repo Repository, dgst digest.Digest, size int64, r io.Reader,
) (bool, error) {
	session, retryable, err := c.openSession(ctx, repo)
	if err != nil {
		return retryable, err
	}
	return c.commitUpload(ctx, session.url, dgst, size, r)
}

// uploadSession is an open blob upload session on a registry.
type uploadSession struct {
	// url is the session URL the next upload request must target.
	url *url.URL
	// minChunk is the registry's OCI-Chunk-Min-Length wish in bytes,
	// or zero when the registry stated none.
	minChunk int64
}

// openSession opens an upload session with POST
// /v2/<name>/blobs/uploads/ and returns it. The bool carries the same
// restartability meaning as pushOnce's.
func (c *Client) openSession(ctx context.Context, repo Repository) (*uploadSession, bool, error) {
	target := &url.URL{
		Scheme: c.scheme(),
		Host:   repo.Host,
		Path:   "/v2/" + repo.Name + "/blobs/uploads/",
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), nil)
	if err != nil {
		return nil, false, fmt.Errorf("building session request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("starting upload session: %w", err)
	}
	if !isSuccess(resp.StatusCode) {
		defer resp.Body.Close()
		regErr := interpretError(resp)
		return nil, retryableRegistryStatus(regErr), fmt.Errorf("starting upload session: %w", regErr)
	}
	drainAndClose(resp.Body)

	sessionURL, err := resolveLocation(target, resp.Header.Get("Location"))
	if err != nil {
		return nil, false, fmt.Errorf("starting upload session: %w", err)
	}
	// The spec spells the header OCI-Chunk-Min-Length; Go canonicalizes
	// the key either way, and header names are case-insensitive on the
	// wire.
	minChunk, _ := strconv.ParseInt(resp.Header.Get("Oci-Chunk-Min-Length"), 10, 64)
	return &uploadSession{url: sessionURL, minChunk: minChunk}, false, nil
}

// commitUpload sends the blob bytes with PUT <session>?digest=<digest>
// and checks that the registry accepted them. The bool carries the
// same restartability meaning as pushOnce's.
func (c *Client) commitUpload(
	ctx context.Context, uploadURL *url.URL, dgst digest.Digest, size int64, r io.Reader,
) (bool, error) {
	// Preserve any session state the registry packed into the
	// Location query (registry:2 uses a _state parameter).
	query := uploadURL.Query()
	query.Set("digest", dgst.String())
	uploadURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL.String(), r)
	if err != nil {
		return false, fmt.Errorf("building commit request: %w", err)
	}
	// NewRequest only infers ContentLength for a few concrete reader
	// types; the spec requires it, so set it from the mandatory size.
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return true, fmt.Errorf("committing upload: %w", err)
	}
	defer resp.Body.Close()

	if !isSuccess(resp.StatusCode) {
		regErr := interpretError(resp)
		return retryableRegistryStatus(regErr), fmt.Errorf("committing upload: %w", regErr)
	}
	return false, nil
}

// rewinder captures r's current position and returns a function that
// seeks back to it, or nil when r cannot seek.
func rewinder(r io.Reader) func() error {
	seeker, ok := r.(io.Seeker)
	if !ok {
		return nil
	}
	start, err := seeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil
	}
	return func() error {
		_, err := seeker.Seek(start, io.SeekStart)
		return err
	}
}

// retryableRegistryStatus reports whether err carries a registry
// status worth retrying.
func retryableRegistryStatus(err error) bool {
	var regErr *registryError
	return errors.As(err, &regErr) && retryableStatus(regErr.status)
}

// retryAfterOf extracts a registry's Retry-After wish from err, or
// zero when it carries none.
func retryAfterOf(err error) time.Duration {
	var regErr *registryError
	if errors.As(err, &regErr) {
		return regErr.retryAfter
	}
	return 0
}

package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/opencontainers/go-digest"
)

// Push uploads a blob monolithically: one POST to open an upload
// session, one PUT to send the bytes and commit them under dgst.
//
// The size is mandatory and must match the number of bytes r yields;
// registries need it as Content-Length, and there is no unknown-length
// upload. A caller that does not know the size spools the data first
// and comes back with a number. The registry rejects the commit when
// the content does not hash to dgst, so a corrupt stream cannot be
// stored silently.
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

	uploadURL, err := c.startUpload(ctx, repo, "")
	if err != nil {
		return fmt.Errorf("starting upload for blob %s in %s/%s: %w", dgst, repo.Host, repo.Name, err)
	}
	if err := c.commitUpload(ctx, uploadURL, dgst, size, r); err != nil {
		return fmt.Errorf("committing blob %s to %s/%s: %w", dgst, repo.Host, repo.Name, err)
	}
	return nil
}

// startUpload opens an upload session with POST
// /v2/<name>/blobs/uploads/ and returns the session URL from the
// Location header, resolved against the request URL. A non-empty
// rawQuery is appended to the POST target (Mount uses it for
// mount/from).
func (c *Client) startUpload(ctx context.Context, repo Repository, rawQuery string) (*url.URL, error) {
	target := &url.URL{
		Scheme:   c.scheme(),
		Host:     repo.Host,
		Path:     "/v2/" + repo.Name + "/blobs/uploads/",
		RawQuery: rawQuery,
	}
	resp, err := c.post(ctx, target)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return resolveLocation(target, resp.Header.Get("Location"))
}

// commitUpload sends the blob bytes with PUT
// <session>?digest=<digest> and checks that the registry accepted
// them.
func (c *Client) commitUpload(
	ctx context.Context, uploadURL *url.URL, dgst digest.Digest, size int64, r io.Reader,
) error {
	// Preserve any session state the registry packed into the
	// Location query (registry:2 uses a _state parameter).
	query := uploadURL.Query()
	query.Set("digest", dgst.String())
	uploadURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL.String(), r)
	if err != nil {
		return fmt.Errorf("building commit request: %w", err)
	}
	// NewRequest only infers ContentLength for a few concrete reader
	// types; the spec requires it, so set it from the mandatory size.
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if !isSuccess(resp.StatusCode) {
		return interpretError(resp)
	}
	return nil
}

// post issues a POST with no body to target and returns the response
// only when the status sits in the 2xx family. On any other status it
// interprets the error body, closes the response, and returns the
// error.
func (c *Client) post(ctx context.Context, target *url.URL) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if !isSuccess(resp.StatusCode) {
		defer resp.Body.Close()
		return nil, interpretError(resp)
	}
	return resp, nil
}

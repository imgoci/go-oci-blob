package blob

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/opencontainers/go-digest"
)

// Mount asks the registry to mount a blob from the src repository
// into dst without moving bytes, via POST
// /v2/<dst>/blobs/uploads/?mount=<digest>&from=<src>.
//
// A 201 Created reports a successful mount. Any other success status
// means the registry declined and opened a regular upload session
// instead; Mount reports that as (false, nil) — not an error — and
// the caller decides whether to Push. Both repositories must live on
// the same registry host.
func (c *Client) Mount(
	ctx context.Context, dst, src Repository, dgst digest.Digest,
) (bool, error) {
	if err := validateTarget(dst, dgst); err != nil {
		return false, fmt.Errorf("invalid mount destination: %w", err)
	}
	if err := src.Validate(); err != nil {
		return false, fmt.Errorf("invalid mount source: %w", err)
	}
	if dst.Host != src.Host {
		return false, fmt.Errorf(
			"cannot mount across registries: destination host %q differs from source host %q",
			dst.Host, src.Host)
	}

	query := url.Values{}
	query.Set("mount", dgst.String())
	query.Set("from", src.Name)
	resp, err := c.post(ctx, &url.URL{
		Scheme:   c.scheme(),
		Host:     dst.Host,
		Path:     "/v2/" + dst.Name + "/blobs/uploads/",
		RawQuery: query.Encode(),
	})
	if err != nil {
		return false, fmt.Errorf("mounting blob %s from %s into %s on %s: %w",
			dgst, src.Name, dst.Name, dst.Host, err)
	}
	defer resp.Body.Close()

	// Only 201 means the blob was mounted. The spec's decline path is
	// a 202 with an upload session; treat every other success the
	// same way so an unexpected-but-successful status degrades to a
	// normal push instead of a false positive.
	return resp.StatusCode == http.StatusCreated, nil
}

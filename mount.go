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
// A 201 Created reports a successful mount. A 202 Accepted means the
// registry declined and opened a regular upload session instead;
// Mount cancels that unused session and reports (false, nil), leaving
// the caller to decide whether to Push. A failed cancellation is returned
// as an error because otherwise the unused session would be leaked. Both
// repositories must live on the same registry host.
func (c *Client) Mount(
	ctx context.Context, dst, src Repository, dgst digest.Digest,
) (bool, error) {
	if err := validateTarget(dst, dgst); err != nil {
		return false, fmt.Errorf("invalid mount destination: %w", err)
	}
	if err := src.Validate(); err != nil {
		return false, fmt.Errorf("invalid mount source: %w", err)
	}
	dstAuthority, err := canonicalRegistryAuthority(dst.Host, c.scheme())
	if err != nil {
		return false, fmt.Errorf("invalid mount destination: %w", err)
	}
	srcAuthority, err := canonicalRegistryAuthority(src.Host, c.scheme())
	if err != nil {
		return false, fmt.Errorf("invalid mount source: %w", err)
	}
	if dstAuthority != srcAuthority {
		return false, fmt.Errorf(
			"cannot mount across registries: destination host %q differs from source host %q",
			dst.Host, src.Host)
	}

	query := url.Values{}
	query.Set("mount", dgst.String())
	query.Set("from", src.Name)
	target := &url.URL{
		Scheme:   c.scheme(),
		Host:     dst.Host,
		Path:     "/v2/" + dst.Name + "/blobs/uploads/",
		RawQuery: query.Encode(),
	}
	resp, err := c.doRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, target.String(), nil)
	})
	if err != nil {
		return false, fmt.Errorf("mounting blob %s from %s into %s on %s: %w",
			dgst, src.Name, dst.Name, dst.Host, err)
	}
	switch resp.StatusCode {
	case http.StatusCreated:
		drainAndClose(resp.Body)
		return true, nil
	case http.StatusAccepted:
		drainAndClose(resp.Body)
		sessionURL, locationErr := resolveLocation(
			responseRequestURL(resp, target), resp.Header.Get("Location"))
		if locationErr != nil {
			return false, fmt.Errorf(
				"mounting blob %s from %s into %s on %s opened an invalid upload session: %w",
				dgst, src.Name, dst.Name, dst.Host, locationErr)
		}
		if cleanupErr := c.cancelUploadSession(
			ctx, &uploadSession{url: sessionURL, registry: target},
		); cleanupErr != nil {
			return false, fmt.Errorf(
				"mounting blob %s from %s into %s on %s declined and cleanup failed: %w",
				dgst, src.Name, dst.Name, dst.Host, cleanupErr)
		}
		return false, nil
	default:
		defer resp.Body.Close()
		return false, fmt.Errorf("mounting blob %s from %s into %s on %s: %w",
			dgst, src.Name, dst.Name, dst.Host, interpretError(resp))
	}
}

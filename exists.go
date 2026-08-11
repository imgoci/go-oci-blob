package blob

import (
	"context"
	"fmt"
	"net/http"

	"github.com/opencontainers/go-digest"
)

// Exists reports whether the repository holds a blob with the given
// digest.
//
// Exists issues a HEAD request to the blob endpoint. A 2xx response
// reports true. A 404 reports false with a nil error: absence is a
// normal answer here, not a failure. Every other outcome is an error.
//
// Example:
//
//	ok, err := client.Exists(ctx, blob.Repository{
//		Host: "localhost:5000",
//		Name: "library/ubuntu",
//	}, dgst)
func (c *Client) Exists(ctx context.Context, repo Repository, dgst digest.Digest) (bool, error) {
	if err := validateTarget(repo, dgst); err != nil {
		return false, err
	}

	u := blobURL(c.scheme(), repo, dgst)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		return false, fmt.Errorf("building existence request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("checking blob existence: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case isSuccess(resp.StatusCode):
		return true, nil
	case resp.StatusCode == http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("checking blob %s in %s/%s: %w",
			dgst, repo.Host, repo.Name, interpretError(resp))
	}
}

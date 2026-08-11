package blob

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
)

// wire.go holds the pure core of the registry protocol: building
// request URLs and interpreting responses. Nothing here performs I/O
// beyond reading an already-received response body, so it all tests
// without a network.

// maxErrorBodySize bounds how much of a registry error body is read
// when interpreting a failed response.
const maxErrorBodySize = 64 << 10

// blobURL builds the registry endpoint URL for a blob:
// <scheme>://<host>/v2/<name>/blobs/<digest>.
func blobURL(scheme string, repo Repository, dgst digest.Digest) *url.URL {
	return &url.URL{
		Scheme: scheme,
		Host:   repo.Host,
		Path:   "/v2/" + repo.Name + "/blobs/" + dgst.String(),
	}
}

// resolveLocation resolves a Location header value against the URL of
// the request that produced it. Registries return both relative and
// absolute forms; both are accepted.
func resolveLocation(base *url.URL, location string) (*url.URL, error) {
	if location == "" {
		return nil, errors.New("registry response carried no Location header")
	}
	ref, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("registry returned unparseable Location %q: %w", location, err)
	}
	return base.ResolveReference(ref), nil
}

// isSuccess reports whether the status code sits in the 2xx family.
// The client reacts to families, not exact codes: a registry that
// returns 200 where the spec says 201 still succeeded.
func isSuccess(code int) bool {
	return code >= http.StatusOK && code < http.StatusMultipleChoices
}

// interpretError turns a non-success registry response into an error.
// It parses the OCI error body when one is present and falls back to
// the status code alone when it is not. The response body is left for
// the caller to close.
func interpretError(resp *http.Response) error {
	var message string
	if resp.Body != nil {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		if err == nil {
			message = parseErrorBody(body)
		}
	}
	return &registryError{
		status:     resp.StatusCode,
		message:    message,
		retryAfter: retryAfterDelay(resp.Header.Get("Retry-After"), time.Now()),
	}
}

// parseErrorBody extracts a printable message from an OCI error body
// ({"errors": [{"code": ..., "message": ...}]}). A body that is not
// that shape yields the empty string: a malformed error body is never
// itself an error.
func parseErrorBody(body []byte) string {
	var parsed struct {
		// Errors is the OCI error envelope's list of error details.
		Errors []struct {
			// Code is the machine-readable OCI error code.
			Code string `json:"code"`
			// Message is the human-readable explanation.
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	parts := make([]string, 0, len(parsed.Errors))
	for _, e := range parsed.Errors {
		switch {
		case e.Code != "" && e.Message != "":
			parts = append(parts, e.Code+": "+e.Message)
		case e.Code != "":
			parts = append(parts, e.Code)
		case e.Message != "":
			parts = append(parts, e.Message)
		}
	}
	return strings.Join(parts, "; ")
}

// parseContentRangeTotal extracts the total blob size from a
// Content-Range header of the form "bytes <start>-<end>/<total>".
// A missing header, an unknown total ("*"), or any other shape
// reports false.
func parseContentRangeTotal(header string) (int64, bool) {
	rest, found := strings.CutPrefix(header, "bytes ")
	if !found {
		return 0, false
	}
	_, totalText, found := strings.Cut(rest, "/")
	if !found {
		return 0, false
	}
	total, err := strconv.ParseInt(totalText, 10, 64)
	if err != nil || total < 0 {
		return 0, false
	}
	return total, true
}

// registryError is an error derived from a registry HTTP response.
type registryError struct {
	// status is the HTTP status code of the failed response.
	status int
	// message is the detail parsed from the OCI error body, or empty
	// when the body was absent or not the OCI error shape.
	message string
	// retryAfter is the wait the registry asked for via Retry-After,
	// or zero when it asked for none.
	retryAfter time.Duration
}

// Error renders the status and any parsed registry detail.
func (e *registryError) Error() string {
	if e.message != "" {
		return fmt.Sprintf("registry returned %d: %s", e.status, e.message)
	}
	return fmt.Sprintf("registry returned %d %s", e.status, http.StatusText(e.status))
}

// Unwrap maps the response status onto the package's sentinel errors
// so callers can branch with [errors.Is].
func (e *registryError) Unwrap() error {
	if e.status == http.StatusNotFound {
		return ErrNotFound
	}
	return nil
}

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
	resolved := base.ResolveReference(ref)
	resolved.Scheme = strings.ToLower(resolved.Scheme)
	if resolved.Scheme != registrySchemeHTTP && resolved.Scheme != registrySchemeHTTPS {
		return nil, fmt.Errorf("registry returned Location %q with unsupported scheme %q",
			location, resolved.Scheme)
	}
	if resolved.Hostname() == "" {
		return nil, fmt.Errorf("registry returned Location %q without a host", location)
	}
	return resolved, nil
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
		origin:     originOfResponse(resp),
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

// contentRange is a validated byte interval reported by a
// Content-Range response header.
type contentRange struct {
	// start is the inclusive first byte in the response body.
	start int64
	// end is the inclusive final byte in the response body.
	end int64
	// total is the complete representation length.
	total int64
}

// parseContentRange parses a satisfied byte Content-Range of the
// form "bytes <start>-<end>/<total>". Unknown totals and intervals
// outside the complete representation report false.
func parseContentRange(header string) (contentRange, bool) {
	unit, rest, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(unit, "bytes") {
		return contentRange{}, false
	}
	intervalText, totalText, found := strings.Cut(rest, "/")
	if !found {
		return contentRange{}, false
	}
	startText, endText, found := strings.Cut(intervalText, "-")
	if !found {
		return contentRange{}, false
	}
	start, err := parseDecimal(startText)
	if err != nil || start < 0 {
		return contentRange{}, false
	}
	end, err := parseDecimal(endText)
	if err != nil || end < start {
		return contentRange{}, false
	}
	total, err := parseDecimal(totalText)
	if err != nil || total <= end {
		return contentRange{}, false
	}
	return contentRange{start: start, end: end, total: total}, true
}

// unsatisfiedRangeTotal parses "bytes */<total>" from a 416 response. A
// malformed or non-byte value returns -1.
func unsatisfiedRangeTotal(header string) int64 {
	unit, rest, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found || !strings.EqualFold(unit, "bytes") {
		return -1
	}
	interval, totalText, found := strings.Cut(rest, "/")
	if !found || interval != "*" {
		return -1
	}
	total, err := parseDecimal(totalText)
	if err != nil {
		return -1
	}
	return total
}

// parseDecimal parses the HTTP grammar's non-empty DIGIT sequence
// without accepting signs or surrounding whitespace.
func parseDecimal(text string) (int64, error) {
	if text == "" {
		return 0, strconv.ErrSyntax
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return 0, strconv.ErrSyntax
		}
	}
	return strconv.ParseInt(text, 10, 64)
}

// registryError is an error derived from an HTTP response at a registry or
// off-origin storage boundary.
type registryError struct {
	// status is the HTTP status code of the failed response.
	status int
	// message is the detail parsed from the OCI error body, or empty
	// when the body was absent or not the OCI error shape.
	message string
	// retryAfter is the wait the peer asked for via Retry-After,
	// or zero when it asked for none.
	retryAfter time.Duration
	// origin identifies whether the response came from the registry or storage.
	origin responseOrigin
}

// Error renders the status and any parsed registry detail.
func (e *registryError) Error() string {
	if e.message != "" {
		return fmt.Sprintf("registry returned %d: %s", e.status, e.message)
	}
	return fmt.Sprintf("registry returned %d %s", e.status, http.StatusText(e.status))
}

// Unwrap maps registry-origin response statuses onto package sentinel errors.
func (e *registryError) Unwrap() error {
	if e.origin != responseOriginRegistry {
		return nil
	}
	switch e.status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusRequestEntityTooLarge:
		return ErrTooLarge
	default:
		return nil
	}
}

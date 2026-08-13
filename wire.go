package blob

import (
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

// errInvalidLocation reports a structurally unusable peer-selected Location.
var errInvalidLocation = errors.New("invalid registry Location")

// blobURL builds the registry endpoint URL for a blob:
// <scheme>://<host>/v2/<name>/blobs/<digest>.
func blobURL(scheme string, repo Repository, dgst digest.Digest) *url.URL {
	return &url.URL{
		Scheme: scheme,
		Host:   repo.Host,
		Path:   "/v2/" + repo.Name + "/blobs/" + dgst.String(),
	}
}

// resolveLocation resolves a safe Location against base. Registries return
// both relative and absolute forms; both are accepted.
func resolveLocation(base *url.URL, location string) (*url.URL, error) {
	if location == "" {
		return nil, fmt.Errorf("%w: response carried no Location header", errInvalidLocation)
	}
	if base == nil {
		return nil, fmt.Errorf("%w: response request URL is unavailable", errInvalidLocation)
	}
	ref, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("%w: Location is malformed", errInvalidLocation)
	}
	if ref.User != nil {
		return nil, fmt.Errorf("%w: Location contains user information", errInvalidLocation)
	}
	resolved := base.ResolveReference(ref)
	resolved.Scheme = strings.ToLower(resolved.Scheme)
	if resolved.Scheme != registrySchemeHTTP && resolved.Scheme != registrySchemeHTTPS {
		return nil, fmt.Errorf("%w: Location uses an unsupported scheme", errInvalidLocation)
	}
	if strings.EqualFold(base.Scheme, registrySchemeHTTPS) && resolved.Scheme == registrySchemeHTTP {
		return nil, fmt.Errorf("%w: Location downgrades HTTPS to HTTP", errInvalidLocation)
	}
	if resolved.Hostname() == "" {
		return nil, fmt.Errorf("%w: Location has no host", errInvalidLocation)
	}
	resolved.Fragment = ""
	return resolved, nil
}

// interpretError turns a non-success response into a structurally rendered
// error. The bounded response detail is discarded because it is untrusted
// peer-selected text. The response body is left for the caller to close.
func interpretError(resp *http.Response) error {
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodySize))
	}
	return &registryError{
		status:     resp.StatusCode,
		retryAfter: retryAfterDelay(resp.Header.Get("Retry-After"), time.Now()),
		origin:     originOfResponse(resp),
	}
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
	// retryAfter is the wait the peer asked for via Retry-After,
	// or zero when it asked for none.
	retryAfter time.Duration
	// origin identifies whether the response came from the registry or storage.
	origin responseOrigin
}

// Error renders only the response status, never peer-selected body detail.
func (e *registryError) Error() string {
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

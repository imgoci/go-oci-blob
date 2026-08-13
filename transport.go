package blob

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// errMethodChangingRedirect reports a redirect that would turn a registry
// write into a bodyless GET and could therefore create a false success.
var errMethodChangingRedirect = errors.New("registry redirect changes the request method")

// errWriteRedirectRejected reports a caller-disabled write redirect.
var errWriteRedirectRejected = errors.New("registry write redirect rejected")

// errInvalidRedirectTarget reports a redirect outside the supported HTTP(S)
// URL space before any transport receives it.
var errInvalidRedirectTarget = errors.New("registry redirect has an invalid target")

// errTooManyRedirects reports the client's deterministic redirect-loop limit.
var errTooManyRedirects = errors.New("registry request exceeded the redirect limit")

// maxRedirects matches net/http's default redirect limit.
const maxRedirects = 10

// registryOriginKey is the private context key for the registry origin that
// began a request chain.
type registryOriginKey struct{}

// registryOrigin is the normalized origin allowed to use the caller's
// authenticated registry transport.
type registryOrigin struct {
	// scheme distinguishes a TLS origin from its plain-HTTP counterpart.
	scheme string
	// host is the lower-cased hostname without brackets or a port.
	host string
	// port is explicit, with the scheme's default filled in.
	port string
}

// responseOrigin identifies which transport boundary produced a response.
type responseOrigin uint8

const (
	// responseOriginRegistry is the authenticated registry origin.
	responseOriginRegistry responseOrigin = iota
	// responseOriginStorage is an off-origin storage or CDN endpoint.
	responseOriginStorage
)

// scopedTransport sends requests for the registry origin through the caller's
// authenticated transport and every other origin through a separate storage
// transport.
type scopedTransport struct {
	// registry is the caller-supplied, potentially authenticated transport.
	registry http.RoundTripper
	// storage handles off-origin redirects and upload-session locations.
	storage http.RoundTripper
}

// RoundTrip routes req without exposing registry credentials to an off-origin
// blob-storage service.
func (t *scopedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	origin, marked := req.Context().Value(registryOriginKey{}).(registryOrigin)
	target, validTarget := normalizeOrigin(req.URL)
	if marked && validTarget && target == origin {
		return t.registry.RoundTrip(req)
	}

	storageReq := req.Clone(req.Context())
	storageReq.Header = req.Header.Clone()
	storageReq.Header.Del("Authorization")
	storageReq.Header.Del("Proxy-Authorization")
	storageReq.Header.Del("Cookie")
	storageReq.Header.Del("Cookie2")
	storageReq.Header.Del("Referer")
	return t.storage.RoundTrip(storageReq)
}

// withRegistryOrigin marks req with the registry origin that is allowed to use
// the authenticated transport. Redirect requests inherit this context.
func withRegistryOrigin(req *http.Request, originURL *url.URL) *http.Request {
	origin, ok := normalizeOrigin(originURL)
	if !ok {
		return req
	}
	ctx := context.WithValue(req.Context(), registryOriginKey{}, origin)
	return req.WithContext(ctx)
}

// normalizeOrigin converts an HTTP URL into a comparable scheme, hostname,
// and effective port tuple.
func normalizeOrigin(u *url.URL) (registryOrigin, bool) {
	if u == nil {
		return registryOrigin{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if host == "" || (scheme != registrySchemeHTTP && scheme != registrySchemeHTTPS) {
		return registryOrigin{}, false
	}
	port := u.Port()
	if port == "" {
		if scheme == registrySchemeHTTP {
			port = "80"
		} else {
			port = "443"
		}
	}
	return registryOrigin{scheme: scheme, host: host, port: port}, true
}

// originOfResponse reports whether resp came from the marked registry origin.
// Synthetic responses without request metadata retain the historical
// registry-origin behavior.
func originOfResponse(resp *http.Response) responseOrigin {
	if resp == nil || resp.Request == nil {
		return responseOriginRegistry
	}
	origin, marked := resp.Request.Context().Value(registryOriginKey{}).(registryOrigin)
	target, validTarget := normalizeOrigin(resp.Request.URL)
	if marked && validTarget && target != origin {
		return responseOriginStorage
	}
	return responseOriginRegistry
}

// redirectPolicy returns the redirect policy for one Client.
func redirectPolicy(allowWrite bool) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		return checkRedirect(allowWrite, req, via)
	}
}

// checkRedirect rejects unsafe redirects and applies net/http's default
// ten-hop limit.
func checkRedirect(allowWrite bool, req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("%w: stopped after %d redirects", errTooManyRedirects, maxRedirects)
	}
	if len(via) == 0 {
		return nil
	}
	if _, ok := normalizeOrigin(req.URL); !ok {
		return errInvalidRedirectTarget
	}
	previous := via[len(via)-1]
	if isWriteMethod(previous.Method) && !allowWrite {
		return errWriteRedirectRejected
	}
	if previous.Method != http.MethodGet && previous.Method != http.MethodHead && req.Method != previous.Method {
		return fmt.Errorf("%w: %s became %s", errMethodChangingRedirect, previous.Method, req.Method)
	}
	return nil
}

// isWriteMethod reports whether method can mutate registry or upload state.
func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// retryableRequestError reports transport failures that a fresh request may
// fix; policy-rejected redirects are deterministic and must not be retried.
func retryableRequestError(err error) bool {
	return !errors.Is(err, errMethodChangingRedirect) &&
		!errors.Is(err, errWriteRedirectRejected) &&
		!errors.Is(err, errInvalidRedirectTarget) &&
		!errors.Is(err, errTooManyRedirects) &&
		!malformedRedirectError(err)
}

// malformedRedirectError recognizes the parse failure net/http returns before
// CheckRedirect can inspect a syntactically invalid Location header.
func malformedRedirectError(err error) bool {
	var urlErr *url.Error
	return errors.As(err, &urlErr) && urlErr.Err != nil &&
		strings.HasPrefix(urlErr.Err.Error(), "failed to parse Location header ")
}

// requestError retains a transport cause without rendering its peer-selected
// URL or response text.
type requestError struct {
	// operation identifies the failed request boundary.
	operation string
	// err is the original transport or redirect-policy failure.
	err error
}

// Error renders only the structural operation.
func (e *requestError) Error() string {
	return e.operation + " failed"
}

// Unwrap exposes the original failure for programmatic inspection.
func (e *requestError) Unwrap() error {
	return e.err
}

// doRegistryRequest executes an initial request and marks its own URL as the
// authenticated registry origin for any redirects it follows.
func (c *Client) doRegistryRequest(req *http.Request) (*http.Response, error) {
	// #nosec G704 -- The caller intentionally selects the registry host.
	resp, err := c.httpClient.Do(withRegistryOrigin(req, req.URL))
	if err != nil {
		return resp, &requestError{operation: "registry request", err: err}
	}
	return resp, nil
}

// doLocationRequest executes a request to a registry-provided Location while
// retaining the original registry origin for transport routing.
func (c *Client) doLocationRequest(req *http.Request, originURL *url.URL) (*http.Response, error) {
	// #nosec G704 -- OCI registries are allowed to select absolute storage URLs.
	resp, err := c.httpClient.Do(withRegistryOrigin(req, originURL))
	if err != nil {
		return resp, &requestError{operation: "upload or storage request", err: err}
	}
	return resp, nil
}

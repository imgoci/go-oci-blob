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

// checkRedirect rejects method-changing redirects for registry writes and
// otherwise applies net/http's default ten-hop limit.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("%w: stopped after %d redirects", errTooManyRedirects, maxRedirects)
	}
	if len(via) == 0 {
		return nil
	}
	if _, ok := normalizeOrigin(req.URL); !ok {
		return fmt.Errorf("%w: %s", errInvalidRedirectTarget, req.URL)
	}
	previous := via[len(via)-1]
	if previous.Method != http.MethodGet && previous.Method != http.MethodHead && req.Method != previous.Method {
		return fmt.Errorf("%w: %s became %s", errMethodChangingRedirect, previous.Method, req.Method)
	}
	return nil
}

// retryableRequestError reports transport failures that a fresh request may
// fix; policy-rejected redirects are deterministic and must not be retried.
func retryableRequestError(err error) bool {
	return !errors.Is(err, errMethodChangingRedirect) &&
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

// doRegistryRequest executes an initial request and marks its own URL as the
// authenticated registry origin for any redirects it follows.
func (c *Client) doRegistryRequest(req *http.Request) (*http.Response, error) {
	// The repository host is an intentional caller-selected network target.
	return c.httpClient.Do(withRegistryOrigin(req, req.URL)) //nolint:gosec // The caller selects the registry host.
}

// doLocationRequest executes a request to a registry-provided Location while
// retaining the original registry origin for transport routing.
func (c *Client) doLocationRequest(req *http.Request, originURL *url.URL) (*http.Response, error) {
	// OCI registries are explicitly allowed to return absolute storage URLs.
	return c.httpClient.Do(withRegistryOrigin(req, originURL))
}

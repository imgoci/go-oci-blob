package blob

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/imgoci/go-oci-blob/mocks"
)

// panicRoundTripper makes an accidental typed-nil call visible to tests.
type panicRoundTripper struct{}

// RoundTrip should never be invoked on a typed-nil panicRoundTripper.
func (*panicRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("typed-nil transport was invoked")
}

// requestForTransport builds a request for scoped-transport tests.
func requestForTransport(t *testing.T, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	return req
}

func TestScopedTransportUsesAuthenticatedTransportOnlyForRegistryOrigin(t *testing.T) {
	registry := mocks.NewRoundTripper(t)
	storage := mocks.NewRoundTripper(t)
	transport := &scopedTransport{registry: registry, storage: storage}
	origin, err := url.Parse("https://registry.example/v2/")
	require.NoError(t, err)
	req := withRegistryOrigin(requestForTransport(t, "https://REGISTRY.example:443/blob"), origin)

	registry.EXPECT().
		RoundTrip(mock.Anything).
		Return(&http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("registry")),
		}, nil).
		Once()

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestScopedTransportStripsCredentialsFromOffOriginRequests(t *testing.T) {
	registry := mocks.NewRoundTripper(t)
	storage := mocks.NewRoundTripper(t)
	transport := &scopedTransport{registry: registry, storage: storage}
	origin, err := url.Parse("https://registry.example/v2/")
	require.NoError(t, err)
	req := requestForTransport(t, "https://storage.example/blob")
	req.Header.Set("Authorization", "Bearer registry-secret")
	req.Header.Set("Proxy-Authorization", "Basic proxy-secret")
	req.Header.Set("Cookie", "registry-session=secret")
	req.Header.Set("Referer", "https://registry.example/session?_state=secret")
	req = withRegistryOrigin(req, origin)

	storage.EXPECT().
		RoundTrip(mock.MatchedBy(func(got *http.Request) bool {
			return got.URL.String() == "https://storage.example/blob" &&
				got.Header.Get("Authorization") == "" &&
				got.Header.Get("Proxy-Authorization") == "" &&
				got.Header.Get("Cookie") == "" &&
				got.Header.Get("Referer") == ""
		})).
		Return(&http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("storage")),
		}, nil).
		Once()

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Bearer registry-secret", req.Header.Get("Authorization"),
		"routing must not mutate the caller's request")
	assert.Equal(t, "Basic proxy-secret", req.Header.Get("Proxy-Authorization"),
		"routing must not mutate the caller's request")
	assert.Equal(t, "https://registry.example/session?_state=secret", req.Header.Get("Referer"),
		"routing must not mutate the caller's request")
}

func TestScopedTransportNeverUsesAuthenticatedTransportWithoutAValidOrigin(t *testing.T) {
	registry := mocks.NewRoundTripper(t)
	storage := mocks.NewRoundTripper(t)
	transport := &scopedTransport{registry: registry, storage: storage}
	req := requestForTransport(t, "https://storage.example/blob")
	req.Header.Set("Authorization", "Bearer registry-secret")

	storage.EXPECT().
		RoundTrip(mock.MatchedBy(func(got *http.Request) bool {
			return got.Header.Get("Authorization") == ""
		})).
		Return(&http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("storage")),
		}, nil).
		Once()

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

func TestCheckRedirectRejectsMethodChangingWrites(t *testing.T) {
	tests := []struct {
		name       string
		fromMethod string
		toMethod   string
		wantErr    bool
	}{
		{name: "rejects PUT becoming GET", fromMethod: http.MethodPut, toMethod: http.MethodGet, wantErr: true},
		{name: "rejects PATCH becoming GET", fromMethod: http.MethodPatch, toMethod: http.MethodGet, wantErr: true},
		{name: "allows replayable PUT", fromMethod: http.MethodPut, toMethod: http.MethodPut},
		{name: "allows GET", fromMethod: http.MethodGet, toMethod: http.MethodGet},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := requestForTransport(t, "https://registry.example/source")
			previous.Method = tt.fromMethod
			next := requestForTransport(t, "https://storage.example/target")
			next.Method = tt.toMethod

			err := checkRedirect(next, []*http.Request{previous})
			if tt.wantErr {
				require.ErrorIs(t, err, errMethodChangingRedirect)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCheckRedirectRejectsNonHTTPSTarget(t *testing.T) {
	previous := requestForTransport(t, "https://registry.example/source")
	next := requestForTransport(t, "https://storage.example/target")
	next.URL.Scheme = "gopher"

	err := checkRedirect(next, []*http.Request{previous})

	require.ErrorIs(t, err, errInvalidRedirectTarget)
}

func TestRedirectPolicyErrorsAreNotRetryable(t *testing.T) {
	assert.False(t, retryableRequestError(errMethodChangingRedirect))
	assert.False(t, retryableRequestError(errInvalidRedirectTarget))
	assert.False(t, retryableRequestError(errTooManyRedirects))
	assert.True(t, retryableRequestError(&url.Error{}), "a malformed error value must not panic")
}

func TestTransportOptionsIgnoreTypedNilValues(t *testing.T) {
	var typedNil *panicRoundTripper
	registryDefault := mocks.NewRoundTripper(t)
	storageDefault := mocks.NewRoundTripper(t)
	opts := options{transport: registryDefault, storageTransport: storageDefault}

	WithTransport(typedNil)(&opts)
	WithStorageTransport(typedNil)(&opts)

	assert.Same(t, registryDefault, opts.transport)
	assert.Same(t, storageDefault, opts.storageTransport)
}

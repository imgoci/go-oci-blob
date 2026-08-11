package blob_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
	"github.com/imgoci/go-oci-blob/mocks"
)

// testContext bundles the mocked transport with the client under test.
type testContext struct {
	transport *mocks.RoundTripper
	client    *blob.Client
}

// newTestContext wires a client to a mockery transport, applying any
// extra options after the transport injection. Retries are off by
// default so scripted conversations stay single-shot; retry tests
// opt back in with an explicit policy.
func newTestContext(t *testing.T, opts ...blob.Option) *testContext {
	t.Helper()

	transport := mocks.NewRoundTripper(t)
	opts = append([]blob.Option{
		blob.WithTransport(transport),
		blob.WithRetryPolicy(blob.RetryPolicy{}),
	}, opts...)
	return &testContext{
		transport: transport,
		client:    blob.New(opts...),
	}
}

// response builds a minimal [http.Response] for a scripted round trip.
func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}
}

// headRequestFor matches a round-tripped HEAD request by full URL.
func headRequestFor(urlStr string) any {
	return mock.MatchedBy(func(req *http.Request) bool {
		return req.Method == http.MethodHead && req.URL.String() == urlStr
	})
}

func TestClientExists(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("hello")
	blobEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	tests := []struct {
		name       string
		opts       []blob.Option
		setupMocks func(tc *testContext)
		wantExists bool
		wantErr    string
	}{
		{
			name: "reports true on 200",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(headRequestFor(blobEndpoint)).
					Return(response(http.StatusOK, ""), nil)
			},
			wantExists: true,
		},
		{
			name: "reports true on any 2xx family member",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(headRequestFor(blobEndpoint)).
					Return(response(http.StatusAccepted, ""), nil)
			},
			wantExists: true,
		},
		{
			name: "reports false without error on 404",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(headRequestFor(blobEndpoint)).
					Return(response(http.StatusNotFound, ""), nil)
			},
			wantExists: false,
		},
		{
			name: "speaks plain http when configured",
			opts: []blob.Option{blob.WithPlainHTTP(true)},
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(headRequestFor(
						"http://registry.example.com/v2/library/ubuntu/blobs/"+dgst.String())).
					Return(response(http.StatusOK, ""), nil)
			},
			wantExists: true,
		},
		{
			name: "surfaces registry detail on a server error",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(headRequestFor(blobEndpoint)).
					Return(response(http.StatusInternalServerError,
						`{"errors":[{"code":"UNKNOWN","message":"boom"}]}`), nil)
			},
			wantErr: "registry returned 500",
		},
		{
			name: "surfaces a transport failure",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(mock.Anything).
					Return(nil, errors.New("connection refused"))
			},
			wantErr: "connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t, tt.opts...)
			tt.setupMocks(tc)

			got, err := tc.client.Exists(t.Context(), repo, dgst)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantExists, got)
		})
	}
}

func TestClientExistsRejectsBadInput(t *testing.T) {
	client := blob.New(blob.WithTransport(mocks.NewRoundTripper(t)))

	t.Run("invalid repository never reaches the wire", func(t *testing.T) {
		_, err := client.Exists(t.Context(),
			blob.Repository{Host: "", Name: "ubuntu"}, digest.FromString("x"))

		assert.ErrorContains(t, err, "host is empty")
	})

	t.Run("invalid digest never reaches the wire", func(t *testing.T) {
		_, err := client.Exists(t.Context(),
			blob.Repository{Host: "r.io", Name: "ubuntu"}, digest.Digest("not-a-digest"))

		assert.ErrorContains(t, err, "invalid digest")
	})
}

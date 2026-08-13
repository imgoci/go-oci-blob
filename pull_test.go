package blob_test

import (
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// getRequestFor matches a round-tripped GET request by full URL and,
// when rangeValue is non-empty, by its Range header.
func getRequestFor(urlStr, rangeValue string) any {
	return mock.MatchedBy(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			req.URL.String() == urlStr &&
			req.Header.Get("Range") == rangeValue
	})
}

// redirectResponse builds a 307 response pointing at location.
func redirectResponse(location string) *http.Response {
	resp := response(http.StatusTemporaryRedirect, "")
	resp.Header.Set("Location", location)
	return resp
}

func TestClientPull(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "pulled blob content"
	dgst := digest.FromString(content)
	blobEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()
	transportErr := errors.New("connection reset")

	tests := []struct {
		name       string
		setupMocks func(tc *testContext)
		wantBody   string
		wantErrIs  error
		wantErr    bool
	}{
		{
			name: "streams and verifies the blob",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "")).
					Return(response(http.StatusOK, content), nil)
			},
			wantBody: content,
		},
		{
			name: "reports ErrDigestMismatch on tampered content",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "")).
					Return(response(http.StatusOK, content+" (tampered)"), nil)
			},
			wantErrIs: blob.ErrDigestMismatch,
		},
		{
			name: "follows an absolute redirect to blob storage",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "")).
					Return(redirectResponse("https://cdn.example.com/storage/abc"), nil).Once()
				tc.transport.EXPECT().
					RoundTrip(getRequestFor("https://cdn.example.com/storage/abc", "")).
					Return(response(http.StatusOK, content), nil).Once()
			},
			wantBody: content,
		},
		{
			name: "follows a relative redirect on the registry host",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "")).
					Return(redirectResponse("/storage/abc"), nil).Once()
				tc.transport.EXPECT().
					RoundTrip(getRequestFor("https://registry.example.com/storage/abc", "")).
					Return(response(http.StatusOK, content), nil).Once()
			},
			wantBody: content,
		},
		{
			name: "maps a missing blob onto ErrNotFound",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "")).
					Return(response(http.StatusNotFound,
						`{"errors":[{"code":"BLOB_UNKNOWN","message":"blob unknown to registry"}]}`), nil)
			},
			wantErrIs: blob.ErrNotFound,
		},
		{
			name: "tolerates a garbage error body",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "")).
					Return(response(http.StatusInternalServerError, "<html>oops</html>"), nil)
			},
			wantErr: true,
		},
		{
			name: "rejects a non-terminal success status",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "")).
					Return(response(http.StatusAccepted, ""), nil)
			},
			wantErr: true,
		},
		{
			name: "surfaces a transport failure",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(mock.Anything).
					Return(nil, transportErr)
			},
			wantErrIs: transportErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t)
			tt.setupMocks(tc)

			rc, err := tc.client.Pull(t.Context(), repo, dgst)

			if tt.wantErr || tt.wantErrIs != nil {
				if rc != nil {
					// A verification failure only surfaces at end of stream.
					_, readErr := io.ReadAll(rc)
					require.NoError(t, rc.Close())
					err = errors.Join(err, readErr)
				}
				if tt.wantErrIs != nil {
					require.ErrorIs(t, err, tt.wantErrIs)
				}
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			got, err := io.ReadAll(rc)
			require.NoError(t, err)
			require.NoError(t, rc.Close())
			assert.Equal(t, tt.wantBody, string(got))
		})
	}
}

func TestClientPullRange(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	blobEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	tests := []struct {
		name           string
		offset, length int64
		setupMocks     func(tc *testContext)
		wantBody       string
		wantErr        bool
		wantErrIs      error
	}{
		{
			name:   "carves the window when the registry ignores the range",
			offset: 5, length: 7,
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "bytes=5-11")).
					Return(response(http.StatusOK, content), nil)
			},
			wantBody: content[5:12],
		},
		{
			name:   "carves a zero-offset window when the registry ignores the range",
			offset: 0, length: 4,
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "bytes=0-3")).
					Return(response(http.StatusOK, content), nil)
			},
			wantBody: content[:4],
		},
		{
			name:   "errors when the ignored-range fallback cannot reach the offset",
			offset: 100, length: 5,
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "bytes=100-104")).
					Return(response(http.StatusOK, content), nil)
			},
			wantErrIs: io.EOF,
		},
		{
			name:   "rejects a non-terminal success status",
			offset: 0, length: 5,
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(getRequestFor(blobEndpoint, "bytes=0-4")).
					Return(response(http.StatusAccepted, ""), nil)
			},
			wantErr: true,
		},
		{
			name:   "rejects a negative offset without touching the wire",
			offset: -1, length: 5,
			setupMocks: func(_ *testContext) {},
			wantErr:    true,
		},
		{
			name:   "rejects a non-positive length without touching the wire",
			offset: 0, length: 0,
			setupMocks: func(_ *testContext) {},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t)
			tt.setupMocks(tc)

			rc, err := tc.client.PullRange(t.Context(), repo, dgst, tt.offset, tt.length)

			if tt.wantErr || tt.wantErrIs != nil {
				require.Error(t, err)
				if tt.wantErrIs != nil {
					require.ErrorIs(t, err, tt.wantErrIs)
				}
				return
			}
			require.NoError(t, err)
			got, err := io.ReadAll(rc)
			require.NoError(t, err)
			require.NoError(t, rc.Close())
			assert.Equal(t, tt.wantBody, string(got))
		})
	}
}

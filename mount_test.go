package blob_test

import (
	"net/http"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

func TestClientMount(t *testing.T) {
	dst := blob.Repository{Host: "registry.example.com", Name: "mirror/ubuntu"}
	src := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("mounted blob")
	mountEndpoint := "https://registry.example.com/v2/mirror/ubuntu/blobs/uploads/" +
		"?from=library%2Fubuntu&mount=sha256%3A" + dgst.Encoded()

	tests := []struct {
		name          string
		setupMocks    func(tc *testContext)
		wantMounted   bool
		wantError     bool
		wantErrDetail string
	}{
		{
			name: "reports a mount on 201",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(postRequestFor(mountEndpoint)).
					Return(sessionResponse(http.StatusCreated, "/v2/mirror/ubuntu/blobs/abc"), nil)
			},
			wantMounted: true,
		},
		{
			name: "reports a decline when the registry opens an upload session instead",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(postRequestFor(mountEndpoint)).
					Return(sessionResponse(http.StatusAccepted,
						"/v2/mirror/ubuntu/blobs/uploads/session-1"), nil)
				expectDelete(tc,
					"https://registry.example.com/v2/mirror/ubuntu/blobs/uploads/session-1")
			},
			wantMounted: false,
		},
		{
			name: "rejects any other success status",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(postRequestFor(mountEndpoint)).
					Return(response(http.StatusOK, ""), nil)
			},
			wantError: true,
		},
		{
			name: "surfaces registry errors",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(postRequestFor(mountEndpoint)).
					Return(response(http.StatusInternalServerError,
						`{"errors":[{"code":"UNKNOWN","message":"boom"}]}`), nil)
			},
			wantError:     true,
			wantErrDetail: "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t)
			tt.setupMocks(tc)

			mounted, err := tc.client.Mount(t.Context(), dst, src, dgst)

			if tt.wantError {
				require.Error(t, err)
				if tt.wantErrDetail != "" {
					require.ErrorContains(t, err, tt.wantErrDetail)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMounted, mounted)
		})
	}
}

func TestClientMountRejectsBadInput(t *testing.T) {
	dgst := digest.FromString("x")

	t.Run("rejects cross-registry mounts without touching the wire", func(t *testing.T) {
		tc := newTestContext(t)

		_, err := tc.client.Mount(t.Context(),
			blob.Repository{Host: "a.example.com", Name: "dst"},
			blob.Repository{Host: "b.example.com", Name: "src"}, dgst)

		require.Error(t, err)
	})

	t.Run("rejects an invalid destination", func(t *testing.T) {
		tc := newTestContext(t)

		_, err := tc.client.Mount(t.Context(),
			blob.Repository{Host: "", Name: "dst"},
			blob.Repository{Host: "a.example.com", Name: "src"}, dgst)

		require.Error(t, err)
	})

	t.Run("rejects an invalid source", func(t *testing.T) {
		tc := newTestContext(t)

		_, err := tc.client.Mount(t.Context(),
			blob.Repository{Host: "a.example.com", Name: "dst"},
			blob.Repository{Host: "a.example.com", Name: "SRC"}, dgst)

		require.Error(t, err)
	})
}

func TestClientMountCanonicalRegistryHost(t *testing.T) {
	dgst := digest.FromString("x")
	tests := []struct {
		name     string
		opts     []blob.Option
		dstHost  string
		srcHost  string
		endpoint string
	}{
		{
			name:     "DNS case and the HTTPS default port identify one registry",
			dstHost:  "REGISTRY.example.com",
			srcHost:  "registry.example.com:443",
			endpoint: "https://REGISTRY.example.com/v2/dst/blobs/uploads/",
		},
		{
			name:     "the HTTP default port identifies one registry",
			opts:     []blob.Option{blob.WithPlainHTTP(true)},
			dstHost:  "registry.example.com",
			srcHost:  "REGISTRY.example.com:80",
			endpoint: "http://registry.example.com/v2/dst/blobs/uploads/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t, tt.opts...)
			mountEndpoint := tt.endpoint + "?from=src&mount=sha256%3A" + dgst.Encoded()
			tc.transport.EXPECT().
				RoundTrip(postRequestFor(mountEndpoint)).
				Return(sessionResponse(http.StatusCreated, "/v2/dst/blobs/"+dgst.String()), nil)

			mounted, err := tc.client.Mount(t.Context(),
				blob.Repository{Host: tt.dstHost, Name: "dst"},
				blob.Repository{Host: tt.srcHost, Name: "src"}, dgst)

			require.NoError(t, err)
			assert.True(t, mounted)
		})
	}
}

func TestClientMountSessionCleanup(t *testing.T) {
	dst := blob.Repository{Host: "registry.example.com", Name: "dst"}
	src := blob.Repository{Host: "registry.example.com", Name: "src"}
	dgst := digest.FromString("x")
	mountEndpoint := "https://registry.example.com/v2/dst/blobs/uploads/" +
		"?from=src&mount=sha256%3A" + dgst.Encoded()

	t.Run("resolves a relative session Location from the redirected POST", func(t *testing.T) {
		tc := newTestContext(t)
		redirectEndpoint := "https://registry.example.com/redirected/mount/"
		redirect := response(http.StatusTemporaryRedirect, "")
		redirect.Header.Set("Location", redirectEndpoint)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(mountEndpoint)).
			Return(redirect, nil).Once()
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(redirectEndpoint)).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				resp := sessionResponse(http.StatusAccepted, "session")
				resp.Request = req
				return resp, nil
			}).Once()
		expectDelete(tc, redirectEndpoint+"session")

		mounted, err := tc.client.Mount(t.Context(), dst, src, dgst)

		require.NoError(t, err)
		assert.False(t, mounted)
	})

	t.Run("returns a cleanup error when DELETE is refused", func(t *testing.T) {
		tc := newTestContext(t)
		sessionURL := "https://registry.example.com/v2/dst/blobs/uploads/refused"
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(mountEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodDelete && req.URL.String() == sessionURL
			})).
			Return(response(http.StatusInternalServerError, ""), nil).Once()

		mounted, err := tc.client.Mount(t.Context(), dst, src, dgst)

		assert.False(t, mounted)
		require.Error(t, err)
	})
}

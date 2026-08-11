package blob_test

import (
	"net/http"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
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
		name        string
		setupMocks  func(tc *testContext)
		wantMounted bool
		wantErr     string
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
			},
			wantMounted: false,
		},
		{
			name: "treats any other success status as a decline",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(postRequestFor(mountEndpoint)).
					Return(response(http.StatusOK, ""), nil)
			},
			wantMounted: false,
		},
		{
			name: "surfaces registry errors",
			setupMocks: func(tc *testContext) {
				tc.transport.EXPECT().
					RoundTrip(postRequestFor(mountEndpoint)).
					Return(response(http.StatusInternalServerError,
						`{"errors":[{"code":"UNKNOWN","message":"boom"}]}`), nil)
			},
			wantErr: "UNKNOWN: boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t)
			tt.setupMocks(tc)

			mounted, err := tc.client.Mount(t.Context(), dst, src, dgst)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
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

		require.ErrorContains(t, err, "cannot mount across registries")
	})

	t.Run("rejects an invalid destination", func(t *testing.T) {
		tc := newTestContext(t)

		_, err := tc.client.Mount(t.Context(),
			blob.Repository{Host: "", Name: "dst"},
			blob.Repository{Host: "a.example.com", Name: "src"}, dgst)

		require.ErrorContains(t, err, "invalid mount destination")
	})

	t.Run("rejects an invalid source", func(t *testing.T) {
		tc := newTestContext(t)

		_, err := tc.client.Mount(t.Context(),
			blob.Repository{Host: "a.example.com", Name: "dst"},
			blob.Repository{Host: "a.example.com", Name: "SRC"}, dgst)

		require.ErrorContains(t, err, "invalid mount source")
	})
}

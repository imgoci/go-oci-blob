package blob_test

import (
	"io"
	"math"
	"net/http"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

func TestNewIgnoresNilOption(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("nil option")
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()
	tc := newTestContext(t, nil)
	tc.transport.EXPECT().
		RoundTrip(headRequestFor(endpoint)).
		Return(response(http.StatusOK, ""), nil).Once()

	exists, err := tc.client.Exists(t.Context(), repo, dgst)

	require.NoError(t, err)
	assert.True(t, exists)
}

func TestWithParallelPullIgnoresUnsafeConfiguration(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "single stream"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	tests := []struct {
		name      string
		workers   int
		chunkSize int64
	}{
		{name: "overflowing memory bound", workers: 2, chunkSize: math.MaxInt64},
		{name: "unallocatable worker count", workers: math.MaxInt, chunkSize: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t, blob.WithParallelPull(tt.workers, tt.chunkSize))
			tc.transport.EXPECT().
				RoundTrip(getRequestFor(endpoint, "")).
				Return(sizedResponse(content), nil).Once()

			rc, err := tc.client.Pull(t.Context(), repo, dgst)
			require.NoError(t, err)
			got, err := io.ReadAll(rc)
			require.NoError(t, err)
			require.NoError(t, rc.Close())
			assert.Equal(t, content, string(got))
		})
	}
}

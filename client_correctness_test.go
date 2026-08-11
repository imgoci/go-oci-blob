package blob_test

import (
	"io"
	"math"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

func TestNewAcceptsNilOption(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.NotNil(t, blob.New(nil))
	})
}

func TestWithParallelPullIgnoresOverflowingMemoryBound(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "single stream"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()
	tc := newTestContext(t, blob.WithParallelPull(2, math.MaxInt64))
	tc.transport.EXPECT().
		RoundTrip(getRequestFor(endpoint, "")).
		Return(sizedResponse(content), nil).Once()

	rc, err := tc.client.Pull(t.Context(), repo, dgst)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, content, string(got))
}

func TestWithParallelPullIgnoresUnallocatableWorkerCount(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "single stream"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()
	tc := newTestContext(t, blob.WithParallelPull(math.MaxInt, 1))
	tc.transport.EXPECT().
		RoundTrip(getRequestFor(endpoint, "")).
		Return(sizedResponse(content), nil).Once()

	assert.NotPanics(t, func() {
		rc, err := tc.client.Pull(t.Context(), repo, dgst)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, content, string(got))
	})
}

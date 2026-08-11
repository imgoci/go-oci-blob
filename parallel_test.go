package blob_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// rangedResponse builds a 206 answer for the window [start, end] of
// content, with the Content-Range header a parallel puller needs.
func rangedResponse(content string, start, end int64) *http.Response {
	resp := response(http.StatusPartialContent, content[start:end+1])
	resp.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)))
	return resp
}

// expectRange scripts the ranged GET for [start, end] of content.
func expectRange(tc *testContext, endpoint, content string, start, end int64) {
	tc.transport.EXPECT().
		RoundTrip(getRequestFor(endpoint, fmt.Sprintf("bytes=%d-%d", start, end))).
		RunAndReturn(func(*http.Request) (*http.Response, error) {
			return rangedResponse(content, start, end), nil
		}).Once()
}

func TestClientPullParallel(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	// 95 bytes: chunk 10 → 10 chunks with a short tail.
	content := strings.Repeat("0123456789", 9) + "tail!"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	newParallelContext := func(t *testing.T, workers int, chunk int64) *testContext {
		t.Helper()
		return newTestContext(t, blob.WithParallelPull(workers, chunk))
	}

	t.Run("emits concurrent chunks in order through the verifying reader", func(t *testing.T) {
		tc := newParallelContext(t, 4, 10)
		for start := int64(0); start < int64(len(content)); start += 10 {
			end := min(start+9, int64(len(content))-1)
			expectRange(tc, endpoint, content, start, end)
		}

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err, "in-order emission must satisfy the digest check")
		require.NoError(t, rc.Close())
		assert.Equal(t, content, string(got))
	})

	t.Run("falls back to the single stream when the probe gets a 200", func(t *testing.T) {
		tc := newParallelContext(t, 4, 10)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=0-9")).
			Return(response(http.StatusOK, content), nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, content, string(got),
			"the probe response body itself must serve as the fallback stream")
	})

	t.Run("retries a chunk whose body breaks mid-read", func(t *testing.T) {
		tc := newTestContext(t,
			blob.WithParallelPull(2, 10), blob.WithRetryPolicy(fastRetry()))
		expectRange(tc, endpoint, content, 0, 9)
		broken := rangedResponse(content, 10, 19)
		broken.Body = brokenBody(content[10:15])
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=10-19")).
			Return(broken, nil).Once()
		expectRange(tc, endpoint, content, 10, 19)
		for start := int64(20); start < int64(len(content)); start += 10 {
			end := min(start+9, int64(len(content))-1)
			expectRange(tc, endpoint, content, start, end)
		}

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err, "a broken chunk body must be refetched")
		require.NoError(t, rc.Close())
		assert.Equal(t, content, string(got))
	})

	t.Run("fails when the registry stops honoring ranges mid-download", func(t *testing.T) {
		tc := newParallelContext(t, 2, 10)
		expectRange(tc, endpoint, content, 0, 9)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=10-19")).
			Return(response(http.StatusOK, content), nil).Once()
		// Later chunks may or may not be requested before the reader
		// hits the failed chunk; keep their workers satisfiable.
		for start := int64(20); start < int64(len(content)); start += 10 {
			end := min(start+9, int64(len(content))-1)
			tc.transport.EXPECT().
				RoundTrip(getRequestFor(endpoint, fmt.Sprintf("bytes=%d-%d", start, end))).
				RunAndReturn(func(*http.Request) (*http.Response, error) {
					return rangedResponse(content, start, end), nil
				}).Maybe()
		}

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.Error(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, content[:10], string(got),
			"bytes from the ignored later range must not be delivered")
	})
}

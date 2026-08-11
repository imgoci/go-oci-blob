package blob_test

// Scripted-conversation tests for progress reporting: cumulative,
// monotonic, and never double-counted across retries, resumes, and
// restarts.

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

// progressLog records WithProgress callbacks and checks invariants.
type progressLog struct {
	dones []int64
	total int64
}

func (p *progressLog) option() blob.TransferOption {
	return blob.WithProgress(func(done, total int64) {
		p.dones = append(p.dones, done)
		p.total = total
	})
}

// assertMonotonic fails if the recorded counts ever move backward.
func (p *progressLog) assertMonotonic(t *testing.T) {
	t.Helper()
	var prev int64
	for _, done := range p.dones {
		require.GreaterOrEqual(t, done, prev, "progress must never move backward: %v", p.dones)
		prev = done
	}
}

// final returns the last reported count, or zero when none arrived.
func (p *progressLog) final() int64 {
	if len(p.dones) == 0 {
		return 0
	}
	return p.dones[len(p.dones)-1]
}

// sizedResponse builds a 200 response whose ContentLength matches the
// body, the way a real registry answers a blob GET.
func sizedResponse(body string) *http.Response {
	resp := response(http.StatusOK, body)
	resp.ContentLength = int64(len(body))
	return resp
}

func TestPullProgress(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	t.Run("reports cumulative bytes and the Content-Length total", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(sizedResponse(content), nil).Once()
		log := &progressLog{}

		rc, err := tc.client.Pull(t.Context(), repo, dgst, log.option())

		require.NoError(t, err)
		_, err = io.Copy(io.Discard, rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		log.assertMonotonic(t)
		assert.Equal(t, int64(len(content)), log.final(), "progress must end at the blob size")
		assert.Equal(t, int64(len(content)), log.total)
	})

	t.Run("does not double-count across a broken-stream resume", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		first := sizedResponse("")
		first.Body = brokenBody(content[:10])
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(first, nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=10-")).
			Return(rangedResponse(content, 10, int64(len(content))-1), nil).Once()
		log := &progressLog{}

		rc, err := tc.client.Pull(t.Context(), repo, dgst, log.option())

		require.NoError(t, err)
		_, err = io.Copy(io.Discard, rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		log.assertMonotonic(t)
		assert.Equal(t, int64(len(content)), log.final(),
			"resumed bytes must count once, not twice")
	})

	t.Run("aggregates parallel chunks into one count ending at the total", func(t *testing.T) {
		tc := newTestContext(t, blob.WithParallelPull(4, 10))
		for start := int64(0); start < int64(len(content)); start += 10 {
			end := min(start+9, int64(len(content))-1)
			expectRange(tc, endpoint, content, start, end)
		}
		log := &progressLog{}

		rc, err := tc.client.Pull(t.Context(), repo, dgst, log.option())

		require.NoError(t, err)
		_, err = io.Copy(io.Discard, rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		log.assertMonotonic(t)
		assert.Equal(t, int64(len(content)), log.final())
		assert.Equal(t, int64(len(content)), log.total,
			"the probe's Content-Range total must reach the callback")
	})
}

func TestPullRangeProgress(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	tc := newTestContext(t)
	tc.transport.EXPECT().
		RoundTrip(getRequestFor(endpoint, "bytes=5-14")).
		Return(rangedResponse(content, 5, 14), nil).Once()
	log := &progressLog{}

	rc, err := tc.client.PullRange(t.Context(), repo, dgst, 5, 10, log.option())

	require.NoError(t, err)
	_, err = io.Copy(io.Discard, rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	log.assertMonotonic(t)
	assert.Equal(t, int64(10), log.final())
	assert.Equal(t, int64(10), log.total, "the requested window length is the total")
}

func TestPushProgress(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "restartable push content"
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	t.Run("stays monotonic across a restarted upload", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				return sessionResponse(http.StatusAccepted, uploadEndpoint+"session"), nil
			}).Times(2)
		var firstPut, secondPut capturedPut
		expectPut(tc, &firstPut, http.StatusServiceUnavailable)
		expectDelete(tc, uploadEndpoint+"session")
		expectPut(tc, &secondPut, http.StatusCreated)
		log := &progressLog{}

		err := tc.client.Push(t.Context(), repo, dgst,
			int64(len(content)), strings.NewReader(content), log.option())

		require.NoError(t, err)
		log.assertMonotonic(t)
		assert.Equal(t, int64(len(content)), log.final(),
			"a restarted upload must not double-count: final equals size, not 2x")
		assert.Equal(t, int64(len(content)), log.total)
	})

	t.Run("reports committed chunks during a chunked upload", func(t *testing.T) {
		tc := newTestContext(t, blob.WithChunkedUpload(8))
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"s1"), nil).Once()
		var p1, p2, p3 capturedPatch
		expectPatch(tc, &p1, http.StatusAccepted, "0-7", "")
		expectPatch(tc, &p2, http.StatusAccepted, "0-15", "")
		expectPatch(tc, &p3, http.StatusAccepted, fmt.Sprintf("0-%d", len(content)-1), "")
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)
		log := &progressLog{}

		err := tc.client.Push(t.Context(), repo, dgst,
			int64(len(content)), strings.NewReader(content), log.option())

		require.NoError(t, err)
		assert.Equal(t, []int64{8, 16, int64(len(content))}, log.dones,
			"each verified ack commits exactly one chunk")
	})
}

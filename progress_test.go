package blob_test

// Scripted-conversation tests for progress reporting: cumulative,
// monotonic, serialized within one transfer, and never double-counted
// across retries, resumes, and restarts.

import (
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// progressLog records WithProgress callbacks and checks invariants.
type progressLog struct {
	// mu protects dones and total from callback concurrency regressions.
	mu sync.Mutex
	// dones contains every cumulative byte count reported by one transfer.
	dones []int64
	// total is the most recently reported transfer size.
	total int64
	// active counts callbacks that have started but not returned.
	active atomic.Int64
	// overlapped records whether two callbacks ran at the same time.
	overlapped atomic.Bool
}

// option returns a progress option that records calls and detects overlap.
func (p *progressLog) option() blob.TransferOption {
	return blob.WithProgress(func(done, total int64) {
		if p.active.Add(1) != 1 {
			p.overlapped.Store(true)
		}
		defer p.active.Add(-1)

		p.mu.Lock()
		defer p.mu.Unlock()
		p.dones = append(p.dones, done)
		p.total = total
	})
}

// assertMonotonic fails if the recorded counts ever move backward.
func (p *progressLog) assertMonotonic(t *testing.T) {
	t.Helper()
	require.Zero(t, p.active.Load(), "callbacks must finish on the transfer path")
	require.False(t, p.overlapped.Load(), "callbacks within one transfer must not overlap")
	dones, _ := p.snapshot()
	var prev int64
	for _, done := range dones {
		require.GreaterOrEqual(t, done, prev, "progress must never move backward: %v", dones)
		prev = done
	}
}

// final returns the last reported count, or zero when none arrived.
func (p *progressLog) final() int64 {
	dones, _ := p.snapshot()
	if len(dones) == 0 {
		return 0
	}
	return dones[len(dones)-1]
}

// snapshot returns a copy of the recorded callback counts and total.
func (p *progressLog) snapshot() ([]int64, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.dones), p.total
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
		_, total := log.snapshot()
		assert.Equal(t, int64(len(content)), total)
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
		_, total := log.snapshot()
		assert.Equal(t, int64(len(content)), total,
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
	_, total := log.snapshot()
	assert.Equal(t, int64(10), total, "the requested window length is the total")
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
		_, total := log.snapshot()
		assert.Equal(t, int64(len(content)), total)
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
		log.assertMonotonic(t)
		dones, _ := log.snapshot()
		assert.Equal(t, []int64{8, 16, int64(len(content))}, dones,
			"each verified ack commits exactly one chunk")
	})
}

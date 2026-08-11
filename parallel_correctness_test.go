package blob_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

func TestParallelPullClose(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	t.Run("interrupts a blocked probe body without hanging", func(t *testing.T) {
		tc := newTestContext(t, blob.WithParallelPull(1, 10))
		body := newBlockingBody()
		probe := rangedResponse(content, 0, 9)
		probe.Body = body
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=0-9")).
			Return(probe, nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, dgst)
		require.NoError(t, err)

		zeroRead := make(chan error, 1)
		go func() {
			n, readErr := rc.Read(nil)
			if n != 0 {
				readErr = fmt.Errorf("zero-length read returned %d bytes", n)
			}
			zeroRead <- readErr
		}()
		select {
		case readErr := <-zeroRead:
			require.NoError(t, readErr, "a zero-length read must not wait for the probe")
		case <-time.After(time.Second):
			t.Fatal("zero-length read blocked on the probe body")
		}

		select {
		case <-body.started:
		case <-time.After(time.Second):
			t.Fatal("probe body read did not start")
		}
		closed := make(chan error, 1)
		go func() { closed <- rc.Close() }()
		select {
		case closeErr := <-closed:
			require.NoError(t, closeErr)
		case <-time.After(time.Second):
			t.Fatal("Close hung behind the blocked probe body")
		}
	})

	t.Run("a progress callback can close its own reader", func(t *testing.T) {
		short := content[:10]
		shortDigest := digest.FromString(short)
		shortEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + shortDigest.String()
		tc := newTestContext(t, blob.WithParallelPull(1, 10))
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(shortEndpoint, "bytes=0-9")).
			Return(rangedResponse(short, 0, 9), nil).Once()

		var rc io.ReadCloser
		callbackClose := make(chan error, 1)
		progress := blob.WithProgress(func(_, _ int64) {
			callbackClose <- rc.Close()
		})
		var err error
		rc, err = tc.client.Pull(t.Context(), repo, shortDigest, progress)
		require.NoError(t, err)

		readDone := make(chan error, 1)
		go func() {
			buf := make([]byte, len(short))
			n, readErr := rc.Read(buf)
			if readErr == nil && n != len(short) {
				readErr = fmt.Errorf("read %d bytes, want %d", n, len(short))
			}
			readDone <- readErr
		}()
		select {
		case closeErr := <-callbackClose:
			require.NoError(t, closeErr)
		case <-time.After(time.Second):
			t.Fatal("progress callback deadlocked in Close")
		}
		select {
		case readErr := <-readDone:
			require.NoError(t, readErr)
		case <-time.After(time.Second):
			t.Fatal("Read did not return after callback Close")
		}
	})
}

// roundTripFunc adapts a function into an HTTP transport.
type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip calls f.
func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// countedBody decrements active exactly once when the client closes it.
type countedBody struct {
	// Reader serves the ranged response bytes.
	io.Reader

	active *atomic.Int64
	once   sync.Once
}

// Close releases the active-body count.
func (b *countedBody) Close() error {
	b.once.Do(func() { b.active.Add(-1) })
	return nil
}

func TestParallelPullWorkerBoundIncludesProbeAndBuffers(t *testing.T) {
	content := "0123456789abcdefghijKLMNOPQRST"
	dgst := digest.FromString(content)
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	var calls atomic.Int64
	var active atomic.Int64
	var maximum atomic.Int64
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		var start, end int64
		_, err := fmt.Sscanf(req.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		require.NoError(t, err)
		resp := rangedResponse(content, start, end)
		resp.Body = &countedBody{Reader: strings.NewReader(content[start : end+1]), active: &active}
		return resp, nil
	})
	client := blob.New(
		blob.WithTransport(transport),
		blob.WithRetryPolicy(blob.RetryPolicy{}),
		blob.WithParallelPull(1, 10),
	)

	rc, err := client.Pull(t.Context(), repo, dgst)
	require.NoError(t, err)
	one := make([]byte, 1)
	_, err = io.ReadFull(rc, one)
	require.NoError(t, err)
	assert.Equal(t, int64(1), calls.Load(),
		"the next body must wait while the probe buffer is still held")
	rest, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, content, string(one)+string(rest))
	assert.LessOrEqual(t, maximum.Load(), int64(1))
}

func TestParallelPullRetryBudget(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	tests := []struct {
		name      string
		status    int
		wantCalls int
		wantErr   string
	}{
		{name: "503 spends exactly MaxAttempts", status: http.StatusServiceUnavailable, wantCalls: 3, wantErr: "503"},
		{name: "404 is not retried", status: http.StatusNotFound, wantCalls: 1, wantErr: "404"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t,
				blob.WithParallelPull(1, 10), blob.WithRetryPolicy(fastRetry()))
			expectRange(tc, endpoint, content, 0, 9)
			tc.transport.EXPECT().
				RoundTrip(getRequestFor(endpoint, "bytes=10-19")).
				RunAndReturn(func(*http.Request) (*http.Response, error) {
					return response(tt.status, ""), nil
				}).Times(tt.wantCalls)

			rc, err := tc.client.Pull(t.Context(), repo, dgst)
			require.NoError(t, err)
			_, err = io.ReadAll(rc)
			require.ErrorContains(t, err, tt.wantErr)
			require.NoError(t, rc.Close())
		})
	}
}

func TestParallelPullFallbackSharesTheProbeRetryBudget(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()
	tc := newTestContext(t,
		blob.WithParallelPull(1, 10), blob.WithRetryPolicy(fastRetry()))
	var requests atomic.Int32
	tc.transport.EXPECT().
		RoundTrip(getRequestFor(endpoint, "bytes=0-9")).
		RunAndReturn(func(*http.Request) (*http.Response, error) {
			if requests.Add(1) < 3 {
				return response(http.StatusServiceUnavailable, ""), nil
			}
			fallback := response(http.StatusOK, "")
			fallback.Body = brokenBody("")
			return fallback, nil
		}).Times(3)

	rc, err := tc.client.Pull(t.Context(), repo, dgst)

	require.NoError(t, err)
	_, err = io.ReadAll(rc)
	require.ErrorContains(t, err, "after 3 request attempts")
	require.NoError(t, rc.Close())
	assert.Equal(t, int32(3), requests.Load(),
		"a broken 200 fallback must not restart the spent probe budget")
}

func TestParallelPullRangeNotSatisfiableBudget(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	emptyDigest := digest.FromString("")
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + emptyDigest.String()

	t.Run("a validated empty response needs no fallback attempt", func(t *testing.T) {
		tc := newTestContext(t,
			blob.WithParallelPull(1, 10),
			blob.WithRetryPolicy(blob.RetryPolicy{MaxAttempts: 1}),
		)
		var active atomic.Int64
		active.Store(1)
		probe := response(http.StatusRequestedRangeNotSatisfiable, "")
		probe.Header.Set("Content-Range", "bytes */0")
		probe.Body = &countedBody{
			Reader: strings.NewReader("registry error body"),
			active: &active,
		}
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=0-9")).
			Return(probe, nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, emptyDigest)

		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		assert.Empty(t, got, "the 416 error body must never be exposed as blob data")
		assert.Zero(t, active.Load(), "the 416 response body must be closed")
	})

	t.Run("an unproven empty response cannot exceed MaxAttempts one", func(t *testing.T) {
		tc := newTestContext(t,
			blob.WithParallelPull(1, 10),
			blob.WithRetryPolicy(blob.RetryPolicy{MaxAttempts: 1}),
		)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=0-9")).
			Return(response(http.StatusRequestedRangeNotSatisfiable, ""), nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, emptyDigest)

		require.ErrorContains(t, err, "request budget exhausted after 1 attempts")
		assert.Nil(t, rc)
	})

	t.Run("a plain fallback receives only the remaining request", func(t *testing.T) {
		tc := newTestContext(t,
			blob.WithParallelPull(1, 10), blob.WithRetryPolicy(fastRetry()))
		var probeRequests atomic.Int32
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=0-9")).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				if probeRequests.Add(1) == 1 {
					return response(http.StatusServiceUnavailable, ""), nil
				}
				return response(http.StatusRequestedRangeNotSatisfiable, ""), nil
			}).Times(2)
		var fallbackRequests atomic.Int32
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				fallbackRequests.Add(1)
				return response(http.StatusServiceUnavailable, ""), nil
			}).Once()

		rc, err := tc.client.Pull(t.Context(), repo, emptyDigest)

		require.ErrorContains(t, err, "registry returned 503")
		assert.Nil(t, rc)
		assert.Equal(t, int32(2), probeRequests.Load())
		assert.Equal(t, int32(1), fallbackRequests.Load(),
			"the 416 fallback must not restart MaxAttempts")
	})

	t.Run("the fallback body retains the probe attempt count", func(t *testing.T) {
		tc := newTestContext(t,
			blob.WithParallelPull(1, 10),
			blob.WithRetryPolicy(blob.RetryPolicy{
				MaxAttempts:  2,
				InitialDelay: time.Nanosecond,
				MaxDelay:     time.Nanosecond,
			}),
		)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=0-9")).
			Return(response(http.StatusRequestedRangeNotSatisfiable, ""), nil).Once()
		fallback := response(http.StatusOK, "")
		fallback.Body = brokenBody("")
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(fallback, nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, emptyDigest)

		require.NoError(t, err)
		_, err = io.ReadAll(rc)
		require.ErrorContains(t, err, "after 2 request attempts")
		require.NoError(t, rc.Close())
	})
}

func TestParallelPullValidatesEveryContentRange(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	t.Run("continues shorter compliant portions without a gap", func(t *testing.T) {
		tc := newTestContext(t, blob.WithParallelPull(2, 10))
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=0-9")).
			Return(rangedResponse(content, 0, 4), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=5-14")).
			Return(rangedResponse(content, 5, 7), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=8-14")).
			Return(rangedResponse(content, 8, 14), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=15-19")).
			Return(rangedResponse(content, 15, 19), nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, dgst)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, content, string(got))
	})

	t.Run("rejects a mismatched probe interval", func(t *testing.T) {
		tc := newTestContext(t, blob.WithParallelPull(1, 10))
		wrong := rangedResponse(content, 1, 9)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=0-9")).
			Return(wrong, nil).Once()

		_, err := tc.client.Pull(t.Context(), repo, dgst)
		require.ErrorContains(t, err, "invalid Content-Range")
	})

	t.Run("rejects a mismatched later interval", func(t *testing.T) {
		tc := newTestContext(t, blob.WithParallelPull(1, 10))
		expectRange(tc, endpoint, content, 0, 9)
		wrong := rangedResponse(content, 11, 19)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=10-19")).
			Return(wrong, nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, dgst)
		require.NoError(t, err)
		_, err = io.ReadAll(rc)
		require.ErrorContains(t, err, "invalid Content-Range")
		require.NoError(t, rc.Close())
	})
}

func TestParallelPullLargeConfigurationDoesNotPreallocate(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "x"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()
	tc := newTestContext(t, blob.WithParallelPull(1, math.MaxInt64))
	tc.transport.EXPECT().
		RoundTrip(getRequestFor(endpoint, "bytes=0-9223372036854775806")).
		Return(rangedResponse(content, 0, 0), nil).Once()

	assert.NotPanics(t, func() {
		rc, err := tc.client.Pull(t.Context(), repo, dgst)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, content, string(got))
	})
}

func TestParallelPullCancellationPrecedesBufferedData(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()
	tc := newTestContext(t, blob.WithParallelPull(1, 10))
	expectRange(tc, endpoint, content, 0, 9)

	ctx, cancel := context.WithCancel(t.Context())
	rc, err := tc.client.Pull(ctx, repo, dgst)
	require.NoError(t, err)
	one := make([]byte, 1)
	_, err = io.ReadFull(rc, one)
	require.NoError(t, err)
	cancel()

	n, err := rc.Read(make([]byte, 1))
	assert.Zero(t, n)
	require.ErrorIs(t, err, context.Canceled,
		"cancellation must win over already-buffered chunk bytes")
	require.NoError(t, rc.Close())
}

func TestParallelPullContextCancellationIdentity(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()
	tc := newTestContext(t,
		blob.WithParallelPull(1, 10), blob.WithRetryPolicy(fastRetry()))
	ctx, cancel := context.WithCancel(t.Context())
	probe := rangedResponse(content, 0, 9)
	probe.Body = io.NopCloser(readerFunc(func([]byte) (int, error) {
		cancel()
		return 0, errors.New("connection reset by peer")
	}))
	tc.transport.EXPECT().
		RoundTrip(getRequestFor(endpoint, "bytes=0-9")).
		Return(probe, nil).Once()

	rc, err := tc.client.Pull(ctx, repo, dgst)
	require.NoError(t, err)
	_, err = rc.Read(make([]byte, 1))
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, rc.Close())
}

// readerFunc adapts a function into an [io.Reader].
type readerFunc func([]byte) (int, error)

// Read calls f.
func (f readerFunc) Read(p []byte) (int, error) {
	return f(p)
}

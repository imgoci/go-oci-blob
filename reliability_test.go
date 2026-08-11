package blob_test

// Scripted-conversation tests for the reliability layer: retries,
// broken-stream resume, and upload restart.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// fastRetry keeps retry tests quick while still exercising the loop.
func fastRetry() blob.RetryPolicy {
	return blob.RetryPolicy{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond,
		MaxDelay:     2 * time.Millisecond,
	}
}

// brokenBody yields prefix and then fails with a connection-reset
// style error instead of EOF.
func brokenBody(prefix string) io.ReadCloser {
	return io.NopCloser(io.MultiReader(
		strings.NewReader(prefix),
		iotest.ErrReader(errors.New("connection reset by peer")),
	))
}

// blockingBody keeps Read blocked until Close interrupts it.
type blockingBody struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

// newBlockingBody constructs a body whose channels expose its read
// lifecycle to a test.
func newBlockingBody() *blockingBody {
	return &blockingBody{
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

// Read blocks until Close is called.
func (b *blockingBody) Read(_ []byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, errors.New("response body closed")
}

// Close interrupts Read and is idempotent.
func (b *blockingBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

// contextErrorBody waits for its context to finish, then simulates a
// stale transport error that must not hide the context error.
type contextErrorBody struct {
	ctx context.Context
}

// Read waits for context completion and returns an unrelated error.
func (b *contextErrorBody) Read(_ []byte) (int, error) {
	<-b.ctx.Done()
	return 0, errors.New("stale connection reset")
}

// Close is a no-op for the synthetic body.
func (b *contextErrorBody) Close() error {
	return nil
}

// rewindFailureReader reads normally but fails when an upload retry seeks back
// to its captured starting position.
type rewindFailureReader struct {
	// reader supplies the upload bytes and the initial position.
	reader *strings.Reader
	// err is returned by every seek after the initial position capture.
	err error
	// seeks counts position operations so the first can succeed.
	seeks int
}

// Read delegates to the underlying reader.
func (r *rewindFailureReader) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

// Seek captures the initial position, then fails retry rewinds.
func (r *rewindFailureReader) Seek(offset int64, whence int) (int64, error) {
	r.seeks++
	if r.seeks > 1 {
		return 0, r.err
	}
	return r.reader.Seek(offset, whence)
}

func TestExistsRetries(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("retry me")
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	t.Run("retries a 503 and then succeeds", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		tc.transport.EXPECT().
			RoundTrip(headRequestFor(endpoint)).
			Return(response(http.StatusServiceUnavailable, ""), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(headRequestFor(endpoint)).
			Return(response(http.StatusOK, ""), nil).Once()

		exists, err := tc.client.Exists(t.Context(), repo, dgst)

		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("honors the server-error status boundaries", func(t *testing.T) {
		tests := []struct {
			name      string
			status    int
			retryable bool
		}{
			{name: "500 is retryable", status: http.StatusInternalServerError, retryable: true},
			{name: "599 is retryable", status: 599, retryable: true},
			{name: "600 is not retryable", status: 600},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
				tc.transport.EXPECT().
					RoundTrip(headRequestFor(endpoint)).
					Return(response(tt.status, ""), nil).Once()
				if tt.retryable {
					tc.transport.EXPECT().
						RoundTrip(headRequestFor(endpoint)).
						Return(response(http.StatusOK, ""), nil).Once()
				}

				exists, err := tc.client.Exists(t.Context(), repo, dgst)

				if tt.retryable {
					require.NoError(t, err)
					assert.True(t, exists)
					return
				}
				require.Error(t, err)
				assert.False(t, exists)
			})
		}
	})

	t.Run("retries a 429 with a capped Retry-After", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		throttled := response(http.StatusTooManyRequests, "")
		throttled.Header.Set("Retry-After", "30")
		tc.transport.EXPECT().
			RoundTrip(headRequestFor(endpoint)).
			Return(throttled, nil).Once()
		tc.transport.EXPECT().
			RoundTrip(headRequestFor(endpoint)).
			Return(response(http.StatusOK, ""), nil).Once()

		start := time.Now()
		exists, err := tc.client.Exists(t.Context(), repo, dgst)

		require.NoError(t, err)
		assert.True(t, exists)
		assert.Less(t, time.Since(start), 5*time.Second,
			"MaxDelay must cap the registry's 30s Retry-After wish")
	})

	t.Run("gives up when the attempt budget is spent", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		tc.transport.EXPECT().
			RoundTrip(headRequestFor(endpoint)).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				return response(http.StatusServiceUnavailable, ""), nil
			}).Times(3)

		_, err := tc.client.Exists(t.Context(), repo, dgst)

		require.Error(t, err)
	})

	t.Run("does not retry a client error", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		tc.transport.EXPECT().
			RoundTrip(headRequestFor(endpoint)).
			Return(response(http.StatusUnauthorized, ""), nil).Once()

		_, err := tc.client.Exists(t.Context(), repo, dgst)

		require.Error(t, err)
	})
}

func TestPullResume(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	t.Run("resumes a broken stream with a ranged request", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		first := response(http.StatusOK, "")
		first.Body = brokenBody(content[:10])
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(first, nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=10-")).
			Return(rangedResponse(content, 10, int64(len(content))-1), nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err, "digest must verify across the resume")
		require.NoError(t, rc.Close())
		assert.Equal(t, content, string(got))
	})

	t.Run("discards the prefix when the registry ignores the resume range", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		first := response(http.StatusOK, "")
		first.Body = brokenBody(content[:10])
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(first, nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=10-")).
			Return(response(http.StatusOK, content), nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err,
			"replayed prefix must be discarded, not double-hashed")
		require.NoError(t, rc.Close())
		assert.Equal(t, content, string(got))
	})

	t.Run("continues after a shorter valid resume interval", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		first := response(http.StatusOK, "")
		first.Body = brokenBody(content[:10])
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(first, nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=10-")).
			Return(partialRangeResponse(content[10:15], "bytes 10-14/20"), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=15-")).
			Return(partialRangeResponse(content[15:], "bytes 15-19/20"), nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err, "the next resume must start after the short interval")
		require.NoError(t, rc.Close())
		assert.Equal(t, content, string(got))
	})

	t.Run("shares one request budget across resume retries", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		first := response(http.StatusOK, "")
		first.Body = brokenBody(content[:10])
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(first, nil).Once()
		var resumeRequests atomic.Int32
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=10-")).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				resumeRequests.Add(1)
				return response(http.StatusServiceUnavailable, ""), nil
			}).Times(2)

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.Error(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, content[:10], string(got))
		assert.Equal(t, int32(2), resumeRequests.Load(),
			"the initial GET and resume attempts must share MaxAttempts")
	})

	t.Run("counts initial GET retries against the resume budget", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		var requests atomic.Int32
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				if requests.Add(1) < 3 {
					return response(http.StatusServiceUnavailable, ""), nil
				}
				final := response(http.StatusOK, "")
				final.Body = brokenBody("")
				return final, nil
			}).Times(3)

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		_, err = io.ReadAll(rc)
		require.Error(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, int32(3), requests.Load(),
			"a broken body must not restart the initial GET budget")
	})

	t.Run("gives up when the stream keeps breaking without progress", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		first := response(http.StatusOK, "")
		first.Body = brokenBody(content[:10])
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(first, nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=10-")).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				resp := rangedResponse(content, 10, int64(len(content))-1)
				resp.Body = brokenBody("")
				return resp, nil
			}).Times(2)

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		_, err = io.ReadAll(rc)
		require.Error(t, err)
		require.NoError(t, rc.Close())
	})

	t.Run("rejects a resumed response starting at the wrong byte", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		first := response(http.StatusOK, "")
		first.Body = brokenBody(content[:10])
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(first, nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=10-")).
			Return(partialRangeResponse(content[:10], "bytes 0-9/20"), nil).Once()

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.Error(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, content[:10], string(got), "wrong-offset resume bytes must not be delivered")
	})
}

func TestPullCloseTerminatesTheStream(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	t.Run("interrupts a blocked read without reopening the download", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		body := newBlockingBody()
		initial := response(http.StatusOK, "")
		initial.Body = body
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(initial, nil).Once()
		var resumeRequests atomic.Int32
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=0-")).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				resumeRequests.Add(1)
				return rangedResponse(content, 0, int64(len(content))-1), nil
			}).Maybe()

		rc, err := tc.client.Pull(t.Context(), repo, dgst)
		require.NoError(t, err)
		readDone := make(chan error, 1)
		go func() {
			_, readErr := rc.Read(make([]byte, 1))
			readDone <- readErr
		}()
		<-body.started

		require.NoError(t, rc.Close())
		select {
		case readErr := <-readDone:
			require.ErrorIs(t, readErr, io.ErrClosedPipe)
		case <-time.After(time.Second):
			t.Fatal("Read remained blocked after Close")
		}
		assert.Zero(t, resumeRequests.Load(), "Close must not trigger a new ranged request")
	})

	t.Run("cancels a blocked resume request", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		initial := response(http.StatusOK, "")
		initial.Body = brokenBody("")
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "")).
			Return(initial, nil).Once()
		requestStarted := make(chan struct{})
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=0-")).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				close(requestStarted)
				<-req.Context().Done()
				return nil, req.Context().Err()
			}).Once()

		rc, err := tc.client.Pull(t.Context(), repo, dgst)
		require.NoError(t, err)
		readDone := make(chan error, 1)
		go func() {
			_, readErr := rc.Read(make([]byte, 1))
			readDone <- readErr
		}()
		select {
		case <-requestStarted:
		case <-time.After(time.Second):
			t.Fatal("resume request did not start")
		}

		require.NoError(t, rc.Close())
		select {
		case readErr := <-readDone:
			require.ErrorIs(t, readErr, io.ErrClosedPipe)
		case <-time.After(time.Second):
			t.Fatal("resume request remained blocked after Close")
		}
	})
}

func TestPullPreservesContextErrors(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "context-bound content"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	tests := []struct {
		name      string
		newCtx    func(t *testing.T) (context.Context, context.CancelFunc)
		wantError error
	}{
		{
			name: "preserves cancellation",
			newCtx: func(t *testing.T) (context.Context, context.CancelFunc) {
				t.Helper()
				return context.WithCancel(t.Context())
			},
			wantError: context.Canceled,
		},
		{
			name: "preserves deadline expiry",
			newCtx: func(t *testing.T) (context.Context, context.CancelFunc) {
				t.Helper()
				return context.WithTimeout(t.Context(), 10*time.Millisecond)
			},
			wantError: context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.newCtx(t)
			defer cancel()
			tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
			initial := response(http.StatusOK, "")
			initial.Body = &contextErrorBody{ctx: ctx}
			tc.transport.EXPECT().
				RoundTrip(getRequestFor(endpoint, "")).
				Return(initial, nil).Once()

			rc, err := tc.client.Pull(ctx, repo, dgst)
			require.NoError(t, err)
			if errors.Is(tt.wantError, context.Canceled) {
				cancel()
			}
			_, err = rc.Read(make([]byte, 1))

			require.ErrorIs(t, err, tt.wantError)
			require.NoError(t, rc.Close())
		})
	}
}

func TestPushRestart(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "restartable push content"
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	expectSession := func(tc *testContext, times int) {
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				return sessionResponse(http.StatusAccepted, uploadEndpoint+"session"), nil
			}).Times(times)
	}

	t.Run("restarts a transient commit failure from byte zero", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		expectSession(tc, 2)
		var firstPut, secondPut capturedPut
		expectPut(tc, &firstPut, http.StatusServiceUnavailable)
		expectDelete(tc, uploadEndpoint+"session")
		expectPut(tc, &secondPut, http.StatusCreated)

		err := tc.client.Push(t.Context(), repo, dgst,
			int64(len(content)), strings.NewReader(content))

		require.NoError(t, err)
		assert.Equal(t, content, firstPut.body)
		assert.Equal(t, content, secondPut.body,
			"restart must rewind the reader and resend every byte")
	})

	t.Run("restarts from the reader's starting position, not zero", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		expectSession(tc, 2)
		var firstPut, secondPut capturedPut
		expectPut(tc, &firstPut, http.StatusServiceUnavailable)
		expectDelete(tc, uploadEndpoint+"session")
		expectPut(tc, &secondPut, http.StatusCreated)

		full := "SKIP!" + content
		reader := strings.NewReader(full)
		_, err := io.CopyN(io.Discard, reader, 5)
		require.NoError(t, err)

		err = tc.client.Push(t.Context(), repo, dgst, int64(len(content)), reader)

		require.NoError(t, err)
		assert.Equal(t, content, secondPut.body,
			"rewind must return to where the reader stood when Push began")
	})

	t.Run("fails cleanly when the reader cannot be re-read", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		expectSession(tc, 1)
		var put capturedPut
		expectPut(tc, &put, http.StatusServiceUnavailable)
		expectDelete(tc, uploadEndpoint+"session")
		rewindErr := errors.New("rewind failed")
		source := &rewindFailureReader{reader: strings.NewReader(content), err: rewindErr}

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), source)

		require.ErrorIs(t, err, rewindErr)
	})

	t.Run("does not restart a non-retryable failure", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		expectSession(tc, 1)
		var put capturedPut
		expectPut(tc, &put, http.StatusBadRequest)
		expectDelete(tc, uploadEndpoint+"session")

		err := tc.client.Push(t.Context(), repo, dgst,
			int64(len(content)), strings.NewReader(content))

		require.Error(t, err)
	})
}

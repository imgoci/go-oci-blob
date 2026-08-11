package blob_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// gatedReadSeeker keeps its first Read active until a test releases it, making
// asynchronous RoundTripper body ownership deterministic under the race detector.
type gatedReadSeeker struct {
	// reader supplies the upload bytes and seek behavior.
	reader *strings.Reader
	// started closes when the first source Read owns the reader.
	started chan struct{}
	// release lets that first source Read complete.
	release chan struct{}
	// gateOnce applies the gate to only the first Read across all attempts.
	gateOnce sync.Once
}

// blockedUploadReader keeps a source Read pending until a test releases it.
type blockedUploadReader struct {
	// started closes when the request body reaches the caller's source.
	started chan struct{}
	// release lets the blocked source Read return.
	release chan struct{}
	// once publishes started exactly once.
	once sync.Once
}

// delayedFailureReadSeeker publishes one source failure only after a transport
// has closed the request body, exercising the retry ownership handoff.
type delayedFailureReadSeeker struct {
	// reader supplies bytes if an incorrect retry reaches a second attempt.
	reader *strings.Reader
	// started closes when the first source Read begins.
	started chan struct{}
	// release lets the first source Read return its configured failure.
	release chan struct{}
	// first applies the delayed failure to exactly one Read.
	first sync.Once
	// err is the source failure the client must preserve.
	err error
}

// newDelayedFailureReadSeeker creates a seekable source with a gated failure.
func newDelayedFailureReadSeeker(content string, err error) *delayedFailureReadSeeker {
	return &delayedFailureReadSeeker{
		reader: strings.NewReader(content), started: make(chan struct{}),
		release: make(chan struct{}), err: err,
	}
}

// Read returns the gated failure once, then delegates to reader.
func (r *delayedFailureReadSeeker) Read(p []byte) (int, error) {
	first := false
	r.first.Do(func() {
		first = true
		close(r.started)
		<-r.release
	})
	if first {
		return 0, r.err
	}
	return r.reader.Read(p)
}

// Seek forwards to the underlying reader.
func (r *delayedFailureReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return r.reader.Seek(offset, whence)
}

// newBlockedUploadReader creates a source whose reads are test-controlled.
func newBlockedUploadReader() *blockedUploadReader {
	return &blockedUploadReader{started: make(chan struct{}), release: make(chan struct{})}
}

// Read blocks until release, then reports EOF without yielding bytes.
func (r *blockedUploadReader) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return 0, io.EOF
}

// newGatedReadSeeker builds a seekable source with a blocked first Read.
func newGatedReadSeeker(content string) *gatedReadSeeker {
	return &gatedReadSeeker{
		reader:  strings.NewReader(content),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

// Read blocks the first source access until release is closed.
func (r *gatedReadSeeker) Read(p []byte) (int, error) {
	r.gateOnce.Do(func() {
		close(r.started)
		<-r.release
	})
	return r.reader.Read(p)
}

// Seek forwards to the underlying [strings.Reader].
func (r *gatedReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return r.reader.Seek(offset, whence)
}

// TestClientPushPreservesCancellationIdentity verifies that a transport error
// cannot hide the caller's cancellation and cleanup still gets its own window.
func TestClientPushPreservesCancellationIdentity(t *testing.T) {
	const content = "cancel this upload"
	transportErr := errors.New("connection refused")
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	sessionURL := uploadEndpoint + "cancel-me"
	tc := newTestContext(t, blob.WithRetryPolicy(blob.RetryPolicy{MaxAttempts: 3}))
	ctx, cancel := context.WithCancel(t.Context())
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_, err := readAndCloseRequestBody(req)
			if err != nil {
				return nil, err
			}
			cancel()
			return nil, transportErr
		}).Once()
	expectDelete(tc, sessionURL)

	err := tc.client.Push(ctx, repo, dgst, int64(len(content)), strings.NewReader(content))

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, transportErr)
}

// TestClientExistsPreservesContextIdentity checks cancellation and deadline
// errors through the public existence check.
func TestClientExistsPreservesContextIdentity(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("context identity")
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	t.Run("preserves cancellation alongside the transport failure", func(t *testing.T) {
		transportErr := errors.New("connection refused")
		tc := newTestContext(t, blob.WithRetryPolicy(blob.RetryPolicy{MaxAttempts: 3}))
		ctx, cancel := context.WithCancel(t.Context())
		tc.transport.EXPECT().
			RoundTrip(headRequestFor(endpoint)).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				cancel()
				return nil, transportErr
			}).Once()

		_, err := tc.client.Exists(ctx, repo, dgst)

		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, transportErr)
	})

	t.Run("returns an expired deadline before touching the wire", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(blob.RetryPolicy{MaxAttempts: 3}))
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()

		_, err := tc.client.Exists(ctx, repo, dgst)

		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

// TestClientPushProgressCommitsOnlySuccessfulAttempt verifies that failed
// monolithic bodies never become observable committed progress.
func TestClientPushProgressCommitsOnlySuccessfulAttempt(t *testing.T) {
	const content = "retry progress"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	sessionURL := uploadEndpoint + "progress"
	policy := blob.RetryPolicy{MaxAttempts: 2, InitialDelay: time.Nanosecond, MaxDelay: time.Nanosecond}
	tc := newTestContext(t, blob.WithRetryPolicy(policy))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		RunAndReturn(func(*http.Request) (*http.Response, error) {
			return sessionResponse(http.StatusAccepted, sessionURL), nil
		}).Times(2)
	var callbacks []int64
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_, err := readAndCloseRequestBody(req)
			assert.Empty(t, callbacks, "a failed attempt must not report committed progress")
			return response(http.StatusServiceUnavailable, ""), err
		}).Once()
	expectDelete(tc, sessionURL)
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_, err := readAndCloseRequestBody(req)
			assert.Empty(t, callbacks, "progress becomes committed only after the 201 response")
			return response(http.StatusCreated, ""), err
		}).Once()

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content),
		blob.WithProgress(func(done, total int64) {
			assert.Equal(t, int64(len(content)), total)
			callbacks = append(callbacks, done)
		}))

	require.NoError(t, err)
	assert.Equal(t, []int64{int64(len(content))}, callbacks)
}

// TestClientPushWaitsForAsynchronousRequestBodyOwnership proves a valid
// RoundTripper cannot race an upload restart by consuming Body after returning.
func TestClientPushWaitsForAsynchronousRequestBodyOwnership(t *testing.T) {
	const content = "asynchronously consumed upload body"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	sessionURL := uploadEndpoint + "async"
	policy := blob.RetryPolicy{MaxAttempts: 2, InitialDelay: time.Nanosecond, MaxDelay: time.Nanosecond}
	tc := newTestContext(t, blob.WithRetryPolicy(policy))
	source := newGatedReadSeeker(content)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).
		Times(2)
	bodyDone := make(chan struct{})
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			go func() {
				_, _ = io.ReadAll(req.Body)
				_ = req.Body.Close()
				close(bodyDone)
			}()
			<-source.started
			time.AfterFunc(10*time.Millisecond, func() { close(source.release) })
			return response(http.StatusServiceUnavailable, ""), nil
		}).
		Once()
	expectDelete(tc, sessionURL)
	var secondPut capturedPut
	expectPut(tc, &secondPut, http.StatusCreated)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), source)

	require.NoError(t, err)
	assert.Equal(t, content, secondPut.body)
	select {
	case <-bodyDone:
	case <-time.After(time.Second):
		t.Fatal("asynchronous request-body consumer did not finish")
	}
}

// TestClientPushWaitsForDelayedAsynchronousBodyOwnership verifies that Push
// does not close or validate a body before a compliant transport's delayed
// read-and-close sequence has finished.
func TestClientPushWaitsForDelayedAsynchronousBodyOwnership(t *testing.T) {
	const content = "delayed asynchronous upload body"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	tc := newTestContext(t)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"delayed"), nil).Once()
	bodyResult := make(chan struct {
		body string
		err  error
	}, 1)
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			go func() {
				time.Sleep(25 * time.Millisecond)
				body, err := readAndCloseRequestBody(req)
				bodyResult <- struct {
					body string
					err  error
				}{body: string(body), err: err}
			}()
			return response(http.StatusCreated, ""), nil
		}).Once()

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.NoError(t, err)
	result := <-bodyResult
	require.NoError(t, result.err)
	assert.Equal(t, content, result.body)
}

// TestClientPushPreservesSourceOwnershipAfterBodyClose proves request-body
// Close is prompt while Push retains a blocked source until its Read exits.
func TestClientPushPreservesSourceOwnershipAfterBodyClose(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("x")
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	sessionURL := uploadEndpoint + "blocked"
	tc := newTestContext(t)
	source := newBlockedUploadReader()
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
	readDone := make(chan struct{})
	closeReturned := make(chan struct{})
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			go func() {
				_, _ = io.ReadAll(req.Body)
				close(readDone)
			}()
			<-source.started
			_ = req.Body.Close()
			close(closeReturned)
			return response(http.StatusServiceUnavailable, ""), nil
		}).Once()
	expectDelete(tc, sessionURL)
	result := make(chan error, 1)
	go func() {
		result <- tc.client.Push(t.Context(), repo, dgst, 1, source)
	}()

	select {
	case <-closeReturned:
	case <-time.After(250 * time.Millisecond):
		close(source.release)
		t.Fatal("request-body Close waited for the caller's blocked source Read")
	}
	select {
	case <-readDone:
	case <-time.After(250 * time.Millisecond):
		close(source.release)
		t.Fatal("request-body Close did not unblock the transport's Read")
	}
	select {
	case <-result:
		close(source.release)
		t.Fatal("Push returned while a request was still reading the caller's source")
	case <-time.After(25 * time.Millisecond):
	}
	close(source.release)
	err := <-result
	require.Error(t, err)
}

// TestClientPushCancellationPreservesBlockedSourceOwnership verifies context
// identity survives while Push waits for an in-flight source Read to finish.
func TestClientPushCancellationPreservesBlockedSourceOwnership(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("x")
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	sessionURL := uploadEndpoint + "blocked-cancel"
	tc := newTestContext(t)
	source := newBlockedUploadReader()
	ctx, cancel := context.WithCancel(t.Context())
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
	readDone := make(chan struct{})
	closeReturned := make(chan struct{})
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			go func() {
				_, _ = io.ReadAll(req.Body)
				close(readDone)
			}()
			<-source.started
			<-req.Context().Done()
			_ = req.Body.Close()
			close(closeReturned)
			return nil, errors.New("connection reset")
		}).Once()
	expectDelete(tc, sessionURL)
	result := make(chan error, 1)
	go func() {
		result <- tc.client.Push(ctx, repo, dgst, 1, source)
	}()
	<-source.started
	cancel()

	select {
	case <-closeReturned:
	case <-time.After(250 * time.Millisecond):
		close(source.release)
		t.Fatal("canceled request-body Close waited for the blocked source Read")
	}
	select {
	case <-readDone:
	case <-time.After(250 * time.Millisecond):
		close(source.release)
		t.Fatal("canceled request-body Close did not unblock the transport's Read")
	}
	select {
	case <-result:
		close(source.release)
		t.Fatal("canceled Push returned while still reading the caller's source")
	case <-time.After(25 * time.Millisecond):
	}
	close(source.release)
	err := <-result
	require.ErrorIs(t, err, context.Canceled)
}

// TestClientPushPreCanceledZeroSizeDoesNotReadSource verifies cancellation is
// checked before the exact-EOF preflight can touch a caller reader.
func TestClientPushPreCanceledZeroSizeDoesNotReadSource(t *testing.T) {
	tc := newTestContext(t)
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	source := newBlockedUploadReader()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := tc.client.Push(ctx, repo, digest.FromString(""), 0, source)

	require.ErrorIs(t, err, context.Canceled)
	select {
	case <-source.started:
		t.Fatal("Push read a zero-size source after its context was already canceled")
	default:
	}
}

// TestClientPushZeroLengthBodyReadDoesNotStartSource verifies a transport's
// empty Read obeys [io.Reader] semantics without touching a blocking source.
func TestClientPushZeroLengthBodyReadDoesNotStartSource(t *testing.T) {
	tc := newTestContext(t)
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("x")
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	sessionURL := uploadEndpoint + "zero-read"
	source := newBlockedUploadReader()
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
	var readN int
	var readErr error
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			readN, readErr = req.Body.Read(nil)
			_ = req.Body.Close()
			return response(http.StatusServiceUnavailable, ""), nil
		}).Once()
	expectDelete(tc, sessionURL)

	err := tc.client.Push(t.Context(), repo, dgst, 1, source)

	require.Error(t, err)
	assert.Zero(t, readN)
	require.NoError(t, readErr)
	select {
	case <-source.started:
		t.Fatal("zero-length request-body Read touched the caller's source")
	default:
	}
}

// TestClientPushDoesNotRetrySourceErrorProvenDuringRewind verifies that a
// delayed input failure cannot disappear when ownership is handed to retry.
func TestClientPushDoesNotRetrySourceErrorProvenDuringRewind(t *testing.T) {
	const content = "retry must not erase this source failure"
	sourceErr := errors.New("source failed")
	source := newDelayedFailureReadSeeker(content, sourceErr)
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	sessionURL := uploadEndpoint + "delayed-source-error"
	policy := blob.RetryPolicy{MaxAttempts: 2, InitialDelay: time.Nanosecond, MaxDelay: time.Nanosecond}
	tc := newTestContext(t, blob.WithRetryPolicy(policy))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			go func() { _, _ = io.ReadAll(req.Body) }()
			<-source.started
			_ = req.Body.Close()
			time.AfterFunc(25*time.Millisecond, func() { close(source.release) })
			return response(http.StatusServiceUnavailable, ""), nil
		}).Once()
	expectDelete(tc, sessionURL)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), source)

	require.ErrorIs(t, err, sourceErr)
}

// TestClientPushRedirectDoesNotReplayDelayedSourceError verifies GetBody's
// ownership handoff preserves an input failure that appears after Close.
func TestClientPushRedirectDoesNotReplayDelayedSourceError(t *testing.T) {
	const content = "redirect must not erase this source failure"
	sourceErr := errors.New("redirected source failed")
	source := newDelayedFailureReadSeeker(content, sourceErr)
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	sessionURL := uploadEndpoint + "delayed-redirect-source-error"
	tc := newTestContext(t)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
	redirect := response(http.StatusTemporaryRedirect, "")
	redirect.Header.Set("Location", "https://registry.example.com/must-not-be-reached")
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			go func() { _, _ = io.ReadAll(req.Body) }()
			<-source.started
			_ = req.Body.Close()
			time.AfterFunc(25*time.Millisecond, func() { close(source.release) })
			return redirect, nil
		}).Once()
	expectDelete(tc, sessionURL)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), source)

	require.ErrorIs(t, err, sourceErr)
}

// TestClientPushRetriesEmptyNonSeekableReader verifies that a bodyless upload
// does not demand seek support merely because its session must restart.
func TestClientPushRetriesEmptyNonSeekableReader(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("")
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	sessionURL := uploadEndpoint + "empty-retry"
	policy := blob.RetryPolicy{MaxAttempts: 2, InitialDelay: time.Nanosecond, MaxDelay: time.Nanosecond}
	tc := newTestContext(t, blob.WithRetryPolicy(policy))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).
		Times(2)
	var firstPut, secondPut capturedPut
	expectPut(tc, &firstPut, http.StatusServiceUnavailable)
	expectDelete(tc, sessionURL)
	expectPut(tc, &secondPut, http.StatusCreated)

	err := tc.client.Push(t.Context(), repo, dgst, 0, io.LimitReader(strings.NewReader(""), 0))

	require.NoError(t, err)
	assert.True(t, firstPut.noBody)
	assert.True(t, secondPut.noBody)
}

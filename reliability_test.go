package blob_test

// Scripted-conversation tests for the reliability layer: retries,
// broken-stream resume, and upload restart.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
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

		require.ErrorContains(t, err, "registry returned 503")
	})

	t.Run("does not retry a client error", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		tc.transport.EXPECT().
			RoundTrip(headRequestFor(endpoint)).
			Return(response(http.StatusUnauthorized, ""), nil).Once()

		_, err := tc.client.Exists(t.Context(), repo, dgst)

		require.ErrorContains(t, err, "registry returned 401")
	})

	t.Run("stops retrying when the context is canceled", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		ctx, cancel := context.WithCancel(t.Context())
		tc.transport.EXPECT().
			RoundTrip(headRequestFor(endpoint)).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				cancel()
				return nil, errors.New("connection refused")
			}).Once()

		_, err := tc.client.Exists(ctx, repo, dgst)

		require.ErrorContains(t, err, "connection refused")
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
			Return(response(http.StatusPartialContent, content[10:]), nil).Once()

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
				resp := response(http.StatusPartialContent, "")
				resp.Body = brokenBody("")
				return resp, nil
			}).Times(2)

		rc, err := tc.client.Pull(t.Context(), repo, dgst)

		require.NoError(t, err)
		_, err = io.ReadAll(rc)
		require.ErrorContains(t, err, "kept breaking at byte 10")
		require.NoError(t, rc.Close())
	})
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

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)),
			iotest.OneByteReader(strings.NewReader(content)))

		require.ErrorContains(t, err, "not an io.Seeker")
	})

	t.Run("does not restart a non-retryable failure", func(t *testing.T) {
		tc := newTestContext(t, blob.WithRetryPolicy(fastRetry()))
		expectSession(tc, 1)
		var put capturedPut
		expectPut(tc, &put, http.StatusBadRequest)

		err := tc.client.Push(t.Context(), repo, dgst,
			int64(len(content)), strings.NewReader(content))

		require.ErrorContains(t, err, "registry returned 400")
	})
}

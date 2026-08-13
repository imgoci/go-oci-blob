package blob_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

func TestWireProgressCountsTransportConsumptionNotSourceReadAhead(t *testing.T) {
	const consumed = 1024
	content := bytes.Repeat([]byte("x"), 2<<20)
	repo := blob.Repository{Host: "registry.example.com", Name: "library/test"}
	uploadEndpoint := "https://registry.example.com/v2/library/test/blobs/uploads/"
	sessionURL := uploadEndpoint + "partial"
	tc := newTestContext(t)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool { return req.Method == http.MethodPut })).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			body := make([]byte, consumed)
			_, err := io.ReadFull(req.Body, body)
			closeErr := req.Body.Close()
			return response(http.StatusServiceUnavailable, ""), errors.Join(err, closeErr)
		}).Once()
	expectDelete(tc, sessionURL)
	var reported atomic.Int64

	err := tc.client.Push(
		t.Context(),
		repo,
		digest.FromBytes(content),
		int64(len(content)),
		bytes.NewReader(content),
		blob.WithWireProgress(func(delta int64) { reported.Add(delta) }),
	)

	require.Error(t, err)
	assert.Equal(t, int64(consumed), reported.Load())
}

func TestWireProgressIncludesEveryUploadAttempt(t *testing.T) {
	const content = "retry the complete upload"
	const firstConsumed = 5
	repo := blob.Repository{Host: "registry.example.com", Name: "library/test"}
	uploadEndpoint := "https://registry.example.com/v2/library/test/blobs/uploads/"
	firstSession := uploadEndpoint + "first"
	secondSession := uploadEndpoint + "second"
	policy := blob.RetryPolicy{MaxAttempts: 2, InitialDelay: time.Nanosecond, MaxDelay: time.Nanosecond}
	tc := newTestContext(t, blob.WithRetryPolicy(policy))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, firstSession), nil).Once()
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool { return req.Method == http.MethodPut })).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			body := make([]byte, firstConsumed)
			_, err := io.ReadFull(req.Body, body)
			closeErr := req.Body.Close()
			return response(http.StatusServiceUnavailable, ""), errors.Join(err, closeErr)
		}).Once()
	expectDelete(tc, firstSession)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, secondSession), nil).Once()
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool { return req.Method == http.MethodPut })).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_, err := readAndCloseRequestBody(req)
			return response(http.StatusCreated, ""), err
		}).Once()
	var reported atomic.Int64
	var active atomic.Int32
	var overlapped atomic.Bool

	err := tc.client.Push(
		t.Context(),
		repo,
		digest.FromString(content),
		int64(len(content)),
		strings.NewReader(content),
		blob.WithWireProgress(func(delta int64) {
			if active.Add(1) != 1 {
				overlapped.Store(true)
			}
			reported.Add(delta)
			active.Add(-1)
		}),
	)

	require.NoError(t, err)
	assert.Equal(t, int64(firstConsumed+len(content)), reported.Load())
	assert.False(t, overlapped.Load())
}

func TestWireProgressStopsBeforePushReturns(t *testing.T) {
	const content = "asynchronously consumed upload"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/test"}
	uploadEndpoint := "https://registry.example.com/v2/library/test/blobs/uploads/"
	sessionURL := uploadEndpoint + "async"
	tc := newTestContext(t)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
	allowRead := make(chan struct{})
	bodyOwned := make(chan struct{})
	bodyDone := make(chan struct{})
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool { return req.Method == http.MethodPut })).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			go func() {
				close(bodyOwned)
				<-allowRead
				_, _ = io.Copy(io.Discard, req.Body)
				_ = req.Body.Close()
				close(bodyDone)
			}()
			return response(http.StatusServiceUnavailable, ""), nil
		}).Once()
	expectDelete(tc, sessionURL)
	var returned atomic.Bool
	var lateCallback atomic.Bool
	errCh := make(chan error, 1)

	go func() {
		errCh <- tc.client.Push(
			t.Context(),
			repo,
			digest.FromString(content),
			int64(len(content)),
			strings.NewReader(content),
			blob.WithWireProgress(func(int64) {
				if returned.Load() {
					lateCallback.Store(true)
				}
			}),
		)
	}()
	<-bodyOwned
	select {
	case err := <-errCh:
		require.Failf(t, "Push returned early", "error: %v", err)
	default:
	}
	close(allowRead)
	<-bodyDone
	err := <-errCh
	returned.Store(true)

	require.Error(t, err)
	assert.False(t, lateCallback.Load())
}

func TestWireProgressReportsNothingForEmptyBody(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/test"}
	uploadEndpoint := "https://registry.example.com/v2/library/test/blobs/uploads/"
	tc := newTestContext(t)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"empty"), nil).Once()
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool { return req.Method == http.MethodPut })).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_, err := readAndCloseRequestBody(req)
			return response(http.StatusCreated, ""), err
		}).Once()
	var callbacks atomic.Int32

	err := tc.client.Push(
		t.Context(),
		repo,
		digest.FromBytes(nil),
		0,
		bytes.NewReader(nil),
		blob.WithWireProgress(func(int64) { callbacks.Add(1) }),
	)

	require.NoError(t, err)
	assert.Equal(t, int32(0), callbacks.Load())
}

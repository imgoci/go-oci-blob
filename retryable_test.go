package blob_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// retryRoundTripFunc adapts a function to an HTTP transport.
type retryRoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip invokes f.
func (f retryRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// failingReader returns a stable caller-source failure.
type failingReader struct {
	// err is returned from every Read.
	err error
}

// Read returns the configured source failure.
func (r failingReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestClientPushZeroRetryPolicyUsesOneAttempt(t *testing.T) {
	const content = "one attempt"
	var posts atomic.Int32
	var puts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodPost:
			posts.Add(1)
			w.Header().Set("Location", "/upload/session")
			w.WriteHeader(http.StatusAccepted)
		case http.MethodPut:
			puts.Add(1)
			_, _ = io.Copy(io.Discard, req.Body)
			w.WriteHeader(http.StatusServiceUnavailable)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	repo := blob.Repository{Host: strings.TrimPrefix(server.URL, "http://"), Name: "library/test"}
	client := blob.New(blob.WithPlainHTTP(true), blob.WithRetryPolicy(blob.RetryPolicy{}))
	err := client.Push(t.Context(), repo, digest.FromString(content), int64(len(content)), strings.NewReader(content))

	require.Error(t, err)
	assert.Equal(t, int32(1), posts.Load())
	assert.Equal(t, int32(1), puts.Load())
	_, retryable := blob.Retryable(err)
	assert.True(t, retryable)
}

func TestRetryablePreservesOneAttemptFailures(t *testing.T) {
	const retryAfter = 7 * time.Second
	tests := []struct {
		name          string
		registryCode  int
		storageCode   int
		transportErr  error
		wantAfter     time.Duration
		wantStatus    int
		wantRetryable bool
	}{
		{name: "connection failure", transportErr: errors.New("connection reset"), wantRetryable: true},
		{name: "request timeout with active context", transportErr: context.DeadlineExceeded, wantRetryable: true},
		{
			name:          "registry throttling",
			registryCode:  http.StatusTooManyRequests,
			wantAfter:     retryAfter,
			wantStatus:    http.StatusTooManyRequests,
			wantRetryable: true,
		},
		{
			name:          "registry server failure",
			registryCode:  http.StatusServiceUnavailable,
			wantAfter:     retryAfter,
			wantStatus:    http.StatusServiceUnavailable,
			wantRetryable: true,
		},
		{
			name:          "storage unauthorized",
			storageCode:   http.StatusUnauthorized,
			wantAfter:     retryAfter,
			wantStatus:    http.StatusUnauthorized,
			wantRetryable: true,
		},
		{
			name:          "storage forbidden",
			storageCode:   http.StatusForbidden,
			wantAfter:     retryAfter,
			wantStatus:    http.StatusForbidden,
			wantRetryable: true,
		},
		{
			name:          "storage missing",
			storageCode:   http.StatusNotFound,
			wantAfter:     retryAfter,
			wantStatus:    http.StatusNotFound,
			wantRetryable: true,
		},
		{
			name:          "storage location expired",
			storageCode:   http.StatusGone,
			wantAfter:     retryAfter,
			wantStatus:    http.StatusGone,
			wantRetryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runRetryablePush(t, tt.registryCode, tt.storageCode, tt.transportErr)
			wrapped := fmt.Errorf("embedding attempt: %w", fmt.Errorf("push failed: %w", err))

			after, ok := blob.Retryable(wrapped)
			assert.Equal(t, tt.wantRetryable, ok)
			assert.Equal(t, tt.wantAfter, after)
			status, statusOK := blob.StatusCode(wrapped)
			if tt.wantStatus == 0 {
				assert.False(t, statusOK)
			} else {
				assert.True(t, statusOK)
				assert.Equal(t, tt.wantStatus, status)
			}
		})
	}
}

func TestRetryableRejectsTerminalFailures(t *testing.T) {
	t.Run("caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		client := blob.New(blob.WithRetryPolicy(blob.RetryPolicy{}))
		repo := blob.Repository{Host: "registry.example.com", Name: "library/test"}
		err := client.Push(ctx, repo, digest.FromString("x"), 1, strings.NewReader("x"))

		require.Error(t, err)
		_, ok := blob.Retryable(err)
		assert.False(t, ok)
	})

	t.Run("source reader failure", func(t *testing.T) {
		sourceErr := errors.New("source failed")
		transport := retryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.Method {
			case http.MethodPost:
				resp := response(http.StatusAccepted, "")
				resp.Header.Set("Location", "https://registry.example.com/upload/session")
				return resp, nil
			case http.MethodPut:
				_, err := io.Copy(io.Discard, req.Body)
				_ = req.Body.Close()
				return nil, err
			case http.MethodDelete:
				return response(http.StatusNoContent, ""), nil
			default:
				return nil, errors.New("unexpected request")
			}
		})
		client := blob.New(blob.WithTransport(transport), blob.WithRetryPolicy(blob.RetryPolicy{}))
		repo := blob.Repository{Host: "registry.example.com", Name: "library/test"}
		err := client.Push(t.Context(), repo, digest.FromString("x"), 1, failingReader{err: sourceErr})

		require.ErrorIs(t, err, sourceErr)
		_, ok := blob.Retryable(err)
		assert.False(t, ok)
	})
}

func TestRegistryAndStorageSentinels(t *testing.T) {
	tests := []struct {
		name          string
		registryCode  int
		storageCode   int
		wantSentinel  error
		wantRetryable bool
	}{
		{name: "registry 401", registryCode: http.StatusUnauthorized, wantSentinel: blob.ErrUnauthorized},
		{name: "registry 403", registryCode: http.StatusForbidden, wantSentinel: blob.ErrUnauthorized},
		{name: "registry 404", registryCode: http.StatusNotFound, wantSentinel: blob.ErrNotFound},
		{name: "registry 413", registryCode: http.StatusRequestEntityTooLarge, wantSentinel: blob.ErrTooLarge},
		{name: "storage 401", storageCode: http.StatusUnauthorized, wantRetryable: true},
		{name: "storage 403", storageCode: http.StatusForbidden, wantRetryable: true},
		{name: "storage 404", storageCode: http.StatusNotFound, wantRetryable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runRetryablePush(t, tt.registryCode, tt.storageCode, nil)
			require.Error(t, err)
			if tt.wantSentinel != nil {
				require.ErrorIs(t, err, tt.wantSentinel)
			} else {
				require.NotErrorIs(t, err, blob.ErrUnauthorized)
				require.NotErrorIs(t, err, blob.ErrNotFound)
				require.NotErrorIs(t, err, blob.ErrTooLarge)
			}
			_, ok := blob.Retryable(err)
			assert.Equal(t, tt.wantRetryable, ok)
		})
	}
}

// runRetryablePush executes one upload attempt with the requested failure.
func runRetryablePush(t *testing.T, registryCode, storageCode int, transportErr error) error {
	t.Helper()
	const content = "retry classification"
	var storage *httptest.Server
	if storageCode != 0 {
		storage = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Retry-After", "7")
			if req.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = io.Copy(io.Discard, req.Body)
			w.WriteHeader(storageCode)
		}))
		t.Cleanup(storage.Close)
	}

	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if transportErr != nil {
			panic("transport errors do not use the registry server")
		}
		if registryCode != 0 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(registryCode)
			return
		}
		switch req.Method {
		case http.MethodPost:
			w.Header().Set("Location", storage.URL+"/upload/session")
			w.WriteHeader(http.StatusAccepted)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(registry.Close)

	repo := blob.Repository{Host: strings.TrimPrefix(registry.URL, "http://"), Name: "library/test"}
	options := []blob.Option{blob.WithPlainHTTP(true), blob.WithRetryPolicy(blob.RetryPolicy{})}
	if transportErr != nil {
		options = append(options, blob.WithTransport(retryRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportErr
		})))
		repo.Host = "registry.example.com"
	}
	client := blob.New(options...)
	return client.Push(
		t.Context(),
		repo,
		digest.FromString(content),
		int64(len(content)),
		bytes.NewReader([]byte(content)),
	)
}

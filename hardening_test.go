package blob_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

func TestStrictWriteRedirectsReject307And308WithoutSendingTargetRequest(t *testing.T) {
	statuses := []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect}
	for _, status := range statuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			runStrictRedirectPush(t, status, http.MethodPut)
		})
	}
}

func TestStrictWriteRedirectsCoverEveryWriteMethod(t *testing.T) {
	methods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			runStrictRedirectPush(t, http.StatusTemporaryRedirect, method)
		})
	}
}

func TestDefaultWriteRedirectsFollow307And308(t *testing.T) {
	const content = "default redirect behavior"
	statuses := []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect}
	for _, status := range statuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var targetCalls atomic.Int32
			targetResult := make(chan struct {
				body string
				err  error
			}, 1)
			storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				targetCalls.Add(1)
				body, err := io.ReadAll(req.Body)
				targetResult <- struct {
					body string
					err  error
				}{body: string(body), err: err}
				w.WriteHeader(http.StatusCreated)
			}))
			t.Cleanup(storage.Close)

			registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.Method {
				case http.MethodPost:
					w.Header().Set("Location", "/upload/session")
					w.WriteHeader(http.StatusAccepted)
				case http.MethodPut:
					_, _ = io.Copy(io.Discard, req.Body)
					w.Header().Set("Location", storage.URL+"/target")
					w.WriteHeader(status)
				default:
					http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				}
			}))
			t.Cleanup(registry.Close)

			repo := blob.Repository{Host: strings.TrimPrefix(registry.URL, "http://"), Name: "library/test"}
			client := blob.New(blob.WithPlainHTTP(true), blob.WithRetryPolicy(blob.RetryPolicy{}))
			err := client.Push(
				t.Context(), repo, digest.FromString(content), int64(len(content)), strings.NewReader(content))

			require.NoError(t, err)
			result := <-targetResult
			require.NoError(t, result.err)
			assert.Equal(t, content, result.body)
			assert.Equal(t, int32(1), targetCalls.Load())
		})
	}
}

// runStrictRedirectPush verifies strict redirect behavior for method.
func runStrictRedirectPush(t *testing.T, status int, method string) {
	t.Helper()
	const content = "strict redirect behavior"
	var targetCalls atomic.Int32
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(storage.Close)
	signedTarget := storage.URL + "/private/upload?signature=peer-secret#fragment"

	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodPost:
			if method == http.MethodPost {
				w.Header().Set("Location", signedTarget)
				w.WriteHeader(status)
				return
			}
			w.Header().Set("Location", "/upload/session")
			w.WriteHeader(http.StatusAccepted)
		case http.MethodPut:
			_, _ = io.Copy(io.Discard, req.Body)
			if method == http.MethodPut {
				w.Header().Set("Location", signedTarget)
				w.WriteHeader(status)
				return
			}
			if method == http.MethodDelete {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodPatch:
			_, _ = io.Copy(io.Discard, req.Body)
			w.Header().Set("Location", signedTarget)
			w.WriteHeader(status)
		case http.MethodDelete:
			if method == http.MethodDelete {
				w.Header().Set("Location", signedTarget)
				w.WriteHeader(status)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(registry.Close)

	repo := blob.Repository{Host: strings.TrimPrefix(registry.URL, "http://"), Name: "library/test"}
	options := []blob.Option{
		blob.WithPlainHTTP(true),
		blob.WithRetryPolicy(blob.RetryPolicy{}),
		blob.WithWriteRedirects(false),
	}
	if method == http.MethodPatch {
		options = append(options, blob.WithChunkedUpload(int64(len(content))))
	}
	client := blob.New(options...)
	err := client.Push(
		t.Context(), repo, digest.FromString(content), int64(len(content)), strings.NewReader(content))

	require.Error(t, err)
	assert.Equal(t, int32(0), targetCalls.Load())
	assert.NotContains(t, err.Error(), signedTarget)
	assert.NotContains(t, err.Error(), "peer-secret")
	assert.NotContains(t, err.Error(), "fragment")
	_, retryable := blob.Retryable(err)
	assert.False(t, retryable)
}

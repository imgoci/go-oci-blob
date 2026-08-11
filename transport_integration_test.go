package blob_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
	"github.com/imgoci/go-oci-blob/mocks"
)

func TestAbsoluteUploadLocationUsesStorageTransport(t *testing.T) {
	const content = "off-origin upload"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	storageSession := "https://storage.example.com/upload/session?_state=opaque"
	storage := mocks.NewRoundTripper(t)
	tc := newTestContext(t, blob.WithStorageTransport(storage))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			req.Header.Set("Authorization", "Bearer registry-secret")
			return sessionResponse(http.StatusAccepted, storageSession), nil
		}).
		Once()
	storage.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut &&
				strings.HasPrefix(req.URL.String(), storageSession+"&digest=") &&
				req.Header.Get("Authorization") == ""
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			body, err := readAndCloseRequestBody(req)
			assert.Equal(t, content, string(body))
			return response(http.StatusCreated, ""), err
		}).
		Once()

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.NoError(t, err)
}

func TestWriteRedirectUsesStorageTransportWithoutRegistrySecrets(t *testing.T) {
	const content = "redirected upload"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	registrySession := uploadEndpoint + "session?_state=registry-secret"
	storageTarget := "https://storage.example.com/upload/target"
	storage := mocks.NewRoundTripper(t)
	tc := newTestContext(t, blob.WithStorageTransport(storage))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, registrySession), nil).
		Once()
	redirect := response(http.StatusTemporaryRedirect, "")
	redirect.Header.Set("Location", storageTarget)
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			if req.Method != http.MethodPut || !strings.HasPrefix(req.URL.String(), registrySession+"&digest=") {
				return false
			}
			req.Header.Set("Authorization", "Bearer registry-secret")
			req.Header.Set("Cookie", "registry-session=secret")
			return true
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_ = req.Body.Close()
			return redirect, nil
		}).
		Once()
	storage.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut && req.URL.String() == storageTarget &&
				req.Header.Get("Authorization") == "" &&
				req.Header.Get("Cookie") == "" &&
				req.Header.Get("Referer") == ""
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			body, err := readAndCloseRequestBody(req)
			assert.Equal(t, content, string(body))
			return response(http.StatusCreated, ""), err
		}).
		Once()

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.NoError(t, err)
}

func TestWriteRedirectRejectsMethodChange(t *testing.T) {
	const content = "do not turn this PUT into GET"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	registrySession := uploadEndpoint + "session"
	storage := mocks.NewRoundTripper(t)
	tc := newTestContext(t, blob.WithStorageTransport(storage))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, registrySession), nil).
		Once()
	redirect := response(http.StatusFound, "")
	redirect.Header.Set("Location", "https://storage.example.com/upload/target")
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_ = req.Body.Close()
			return redirect, nil
		}).
		Once()
	expectDelete(tc, registrySession)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.ErrorContains(t, err, "changes the request method")
}

func TestWriteRedirectRejectsNonHTTPSTargetBeforeStorageTransport(t *testing.T) {
	const content = "do not send this to another scheme"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	registrySession := uploadEndpoint + "invalid-scheme"
	storage := mocks.NewRoundTripper(t)
	tc := newTestContext(t, blob.WithStorageTransport(storage))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, registrySession), nil).Once()
	redirect := response(http.StatusTemporaryRedirect, "")
	redirect.Header.Set("Location", "gopher://attacker.example/upload")
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_ = req.Body.Close()
			return redirect, nil
		}).Once()
	expectDelete(tc, registrySession)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.ErrorContains(t, err, "invalid target")
}

func TestWriteRedirectRejectsNonHTTPSTargetWithNonSeekableBody(t *testing.T) {
	const content = "non-seekable data for an invalid scheme"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	registrySession := uploadEndpoint + "invalid-scheme-nonseekable"
	storage := mocks.NewRoundTripper(t)
	tc := newTestContext(t, blob.WithStorageTransport(storage))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, registrySession), nil).Once()
	redirect := response(http.StatusTemporaryRedirect, "")
	redirect.Header.Set("Location", "gopher://attacker.example/upload")
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_ = req.Body.Close()
			return redirect, nil
		}).Once()
	expectDelete(tc, registrySession)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), bytes.NewBufferString(content))

	require.ErrorContains(t, err, "invalid target")
	require.ErrorContains(t, err, "unsupported scheme")
}

func TestWriteRedirectLimitDoesNotRestartUpload(t *testing.T) {
	const expectedRedirects = 10
	const content = "stop the redirect loop once"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	registrySession := uploadEndpoint + "redirect-loop"
	policy := blob.RetryPolicy{MaxAttempts: 2}
	tc := newTestContext(t, blob.WithRetryPolicy(policy))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, registrySession), nil).Once()
	redirectCalls := 0
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			redirectCalls++
			_ = req.Body.Close()
			redirect := response(http.StatusTemporaryRedirect, "")
			redirect.Header.Set("Location", "https://registry.example.com/redirect-loop")
			return redirect, nil
		}).Times(expectedRedirects)
	expectDelete(tc, registrySession)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.ErrorContains(t, err, "exceeded the redirect limit")
	assert.Equal(t, expectedRedirects, redirectCalls)
}

func TestMalformedWriteRedirectDoesNotRestartUpload(t *testing.T) {
	const content = "reject malformed redirect once"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	registrySession := uploadEndpoint + "malformed-redirect"
	policy := blob.RetryPolicy{MaxAttempts: 2}
	tc := newTestContext(t, blob.WithRetryPolicy(policy))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, registrySession), nil).Once()
	redirect := response(http.StatusTemporaryRedirect, "")
	redirect.Header.Set("Location", "%zz")
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_ = req.Body.Close()
			return redirect, nil
		}).Once()
	expectDelete(tc, registrySession)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.ErrorContains(t, err, "failed to parse Location header")
}

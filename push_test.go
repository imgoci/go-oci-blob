package blob_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// readAndCloseRequestBody makes mocked RoundTrippers honor net/http's request
// body ownership contract while preserving both read and close failures.
func readAndCloseRequestBody(req *http.Request) ([]byte, error) {
	body, readErr := io.ReadAll(req.Body)
	closeErr := req.Body.Close()
	return body, errors.Join(readErr, closeErr)
}

// postRequestFor matches a round-tripped POST request by full URL.
func postRequestFor(urlStr string) any {
	return mock.MatchedBy(func(req *http.Request) bool {
		return req.Method == http.MethodPost && req.URL.String() == urlStr
	})
}

// sessionResponse builds an upload-session response carrying a
// Location header.
func sessionResponse(status int, location string) *http.Response {
	resp := response(status, "")
	if location != "" {
		resp.Header.Set("Location", location)
	}
	return resp
}

// capturedPut records what the registry saw in the commit PUT.
type capturedPut struct {
	url           string
	body          string
	contentLength int64
	contentType   string
	noBody        bool
	getBody       bool
}

// expectPut scripts the commit PUT on the mocked transport, capturing
// the request for later assertions and answering with status.
func expectPut(tc *testContext, capture *capturedPut, status int) {
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			body, err := readAndCloseRequestBody(req)
			*capture = capturedPut{
				url:           req.URL.String(),
				body:          string(body),
				contentLength: req.ContentLength,
				contentType:   req.Header.Get("Content-Type"),
				noBody:        req.Body == http.NoBody,
				getBody:       req.GetBody != nil,
			}
			if err != nil {
				return nil, err
			}
			return response(status, ""), nil
		}).Once()
}

// expectDelete scripts best-effort cleanup of an abandoned upload session.
func expectDelete(tc *testContext, urlStr string) {
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodDelete && req.URL.String() == urlStr
		})).
		Return(response(http.StatusNoContent, ""), nil).Once()
}

func TestClientPush(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "pushed blob content"
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	t.Run("uploads through the session and commits with the digest", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted,
				uploadEndpoint+"session-1?_state=abc"), nil).Once()
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.NoError(t, err)
		assert.Equal(t, uploadEndpoint+"session-1?_state=abc&digest="+
			"sha256%3A"+dgst.Encoded(), put.url,
			"commit URL should keep the session state and add the digest")
		assert.Equal(t, content, put.body, "commit body should carry the blob bytes")
		assert.Equal(t, int64(len(content)), put.contentLength)
		assert.Equal(t, "application/octet-stream", put.contentType)
		assert.True(t, put.getBody, "seekable monolithic bodies should support 307/308 replay")
	})

	t.Run("rejects an off-spec session success code", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusOK, "/v2/library/ubuntu/blobs/uploads/session-2"), nil).
			Once()

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.ErrorContains(t, err, "registry returned 200")
	})

	t.Run("fails when the session carries no Location", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, ""), nil).Once()

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.ErrorContains(t, err, "no Location header")
	})

	t.Run("surfaces a refused session", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(response(http.StatusInternalServerError,
				`{"errors":[{"code":"UNKNOWN","message":"boom"}]}`), nil).Once()

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.ErrorContains(t, err, "starting upload")
		require.ErrorContains(t, err, "UNKNOWN: boom")
	})

	t.Run("surfaces a failed commit", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"session-3"), nil).Once()
		var put capturedPut
		expectPut(tc, &put, http.StatusBadRequest)
		expectDelete(tc, uploadEndpoint+"session-3")

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.ErrorContains(t, err, "committing upload")
		require.ErrorContains(t, err, "registry returned 400")
	})

	t.Run("rejects a negative size without touching the wire", func(t *testing.T) {
		tc := newTestContext(t)

		err := tc.client.Push(t.Context(), repo, dgst, -1, strings.NewReader(content))

		require.ErrorContains(t, err, "pushes require the exact blob size")
	})

	t.Run("rejects a nil reader without touching the wire", func(t *testing.T) {
		tc := newTestContext(t)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), nil)

		require.ErrorContains(t, err, "nil reader")
	})
}

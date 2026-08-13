package blob_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// capturedPatch records what the registry saw in one chunk PATCH.
type capturedPatch struct {
	url           string
	body          string
	contentRange  string
	contentLength int64
	getBody       bool
}

// expectPatch scripts one chunk PATCH on the mocked transport,
// capturing the request and answering status with the given Range
// acknowledgement and Location headers (empty strings omit them).
func expectPatch(tc *testContext, capture *capturedPatch, status int, ack, location string) {
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPatch
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			body, err := readAndCloseRequestBody(req)
			*capture = capturedPatch{
				url:           req.URL.String(),
				body:          string(body),
				contentRange:  req.Header.Get("Content-Range"),
				contentLength: req.ContentLength,
				getBody:       req.GetBody != nil,
			}
			if err != nil {
				return nil, err
			}
			resp := response(status, "")
			if ack != "" {
				resp.Header.Set("Range", ack)
			}
			if location != "" {
				resp.Header.Set("Location", location)
			}
			return resp, nil
		}).Once()
}

func TestClientPushChunked(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "01234567890123456789"
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	newChunkedContext := func(t *testing.T, chunkSize int64) *testContext {
		t.Helper()
		return newTestContext(t, blob.WithChunkedUpload(chunkSize))
	}

	t.Run("uploads in verified chunks and follows Locations", func(t *testing.T) {
		tc := newChunkedContext(t, 8)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"s1"), nil).Once()
		var p1, p2, p3 capturedPatch
		expectPatch(tc, &p1, http.StatusAccepted, "0-7", "/v2/library/ubuntu/blobs/uploads/s2")
		expectPatch(tc, &p2, http.StatusAccepted, "0-15", uploadEndpoint+"s3")
		expectPatch(tc, &p3, http.StatusAccepted, "0-19", "")
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.NoError(t, err)
		assert.Equal(t, uploadEndpoint+"s1", p1.url)
		assert.Equal(t, "0-7", p1.contentRange)
		assert.Equal(t, content[:8], p1.body)
		assert.Equal(t, int64(8), p1.contentLength)
		assert.True(t, p1.getBody, "seekable chunk bodies should support 307/308 replay")
		assert.Equal(t, uploadEndpoint+"s2", p2.url, "relative Location should resolve")
		assert.Equal(t, "8-15", p2.contentRange)
		assert.Equal(t, content[8:16], p2.body)
		assert.Equal(t, uploadEndpoint+"s3", p3.url, "absolute Location should be followed")
		assert.Equal(t, "16-19", p3.contentRange)
		assert.Equal(t, content[16:], p3.body)
		assert.Equal(t, uploadEndpoint+"s3?digest=sha256%3A"+dgst.Encoded(), put.url,
			"commit should target the last session Location")
		assert.Empty(t, put.body, "commit carries no body; every byte went via PATCH")
		assert.Equal(t, int64(0), put.contentLength)
	})

	t.Run("honors the registry's OCI-Chunk-Min-Length", func(t *testing.T) {
		tc := newChunkedContext(t, 8)
		session := sessionResponse(http.StatusAccepted, uploadEndpoint+"s1")
		session.Header.Set("Oci-Chunk-Min-Length", "12")
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(session, nil).Once()
		var p1, p2 capturedPatch
		expectPatch(tc, &p1, http.StatusAccepted, "0-11", "")
		expectPatch(tc, &p2, http.StatusAccepted, "0-19", "")
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.NoError(t, err)
		assert.Equal(t, "0-11", p1.contentRange, "registry minimum must widen the configured chunk")
		assert.Equal(t, content[:12], p1.body)
		assert.Equal(t, "12-19", p2.contentRange)
		assert.Equal(t, content[12:], p2.body)
	})

	t.Run("abandons the upload when the ack stops advancing", func(t *testing.T) {
		tc := newChunkedContext(t, 8)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"s1"), nil).Once()
		var p1, p2 capturedPatch
		expectPatch(tc, &p1, http.StatusAccepted, "0-7", "")
		// ECR-style: the second chunk is accepted with a success
		// status but the acknowledged range stays where it was.
		expectPatch(tc, &p2, http.StatusAccepted, "0-7", "")
		expectDelete(tc, uploadEndpoint+"s1")

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.Error(t, err)
	})

	t.Run("abandons the upload when the ack is missing", func(t *testing.T) {
		tc := newChunkedContext(t, 8)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"s1"), nil).Once()
		var p1 capturedPatch
		expectPatch(tc, &p1, http.StatusAccepted, "", "")
		expectDelete(tc, uploadEndpoint+"s1")

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.Error(t, err)
	})

	t.Run("restarts the whole upload after a transient chunk failure", func(t *testing.T) {
		tc := newTestContext(t,
			blob.WithChunkedUpload(8), blob.WithRetryPolicy(fastRetry()))
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			RunAndReturn(func(*http.Request) (*http.Response, error) {
				return sessionResponse(http.StatusAccepted, uploadEndpoint+"s1"), nil
			}).Times(2)
		var failed, p1, p2, p3 capturedPatch
		expectPatch(tc, &failed, http.StatusServiceUnavailable, "", "")
		expectDelete(tc, uploadEndpoint+"s1")
		expectPatch(tc, &p1, http.StatusAccepted, "0-7", "")
		expectPatch(tc, &p2, http.StatusAccepted, "0-15", "")
		expectPatch(tc, &p3, http.StatusAccepted, "0-19", "")
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.NoError(t, err)
		assert.Equal(t, content[:8], failed.body)
		assert.Equal(t, content[:8], p1.body,
			"restart must rewind the reader and resend the first chunk")
		assert.Equal(t, content[8:16], p2.body)
		assert.Equal(t, content[16:], p3.body)
	})

	t.Run("commits an empty blob without any chunks", func(t *testing.T) {
		tc := newChunkedContext(t, 8)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"s1"), nil).Once()
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)
		empty := digest.FromString("")

		err := tc.client.Push(t.Context(), repo, empty, 0, strings.NewReader(""))

		require.NoError(t, err)
		assert.Empty(t, put.body)
		assert.Equal(t, int64(0), put.contentLength)
	})
}

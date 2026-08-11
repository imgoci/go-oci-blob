package blob_test

import (
	"bytes"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// countingUploadReader records source reads without changing their behavior.
type countingUploadReader struct {
	// reader supplies the upload bytes.
	reader io.Reader
	// reads counts calls made to the caller-owned source.
	reads atomic.Int64
}

// Read records and forwards one source read.
func (r *countingUploadReader) Read(p []byte) (int, error) {
	r.reads.Add(1)
	return r.reader.Read(p)
}

// TestClientPushBatchesSourceReadsAcrossSmallTransportReads verifies transport
// read granularity does not force a caller-source handoff for every small read.
func TestClientPushBatchesSourceReadsAcrossSmallTransportReads(t *testing.T) {
	const (
		contentSize       = 1 << 20
		transportReadSize = 1 << 10
		maxSourceReads    = 8
	)
	content := bytes.Repeat([]byte("u"), contentSize)
	source := &countingUploadReader{reader: bytes.NewReader(content)}
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	tc := newTestContext(t)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"batched"), nil).Once()
	var transportReads int64
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			buffer := make([]byte, transportReadSize)
			var received int64
			for {
				n, err := req.Body.Read(buffer)
				transportReads++
				received += int64(n)
				if err != nil {
					if err != io.EOF {
						return nil, err
					}
					break
				}
			}
			if err := req.Body.Close(); err != nil {
				return nil, err
			}
			assert.Equal(t, int64(contentSize), received)
			return response(http.StatusCreated, ""), nil
		}).Once()

	err := tc.client.Push(
		t.Context(), repo, digest.FromBytes(content), int64(len(content)), source)

	require.NoError(t, err)
	assert.Greater(t, transportReads, int64(maxSourceReads),
		"the fixture must exercise many small transport reads")
	assert.LessOrEqual(t, source.reads.Load(), int64(maxSourceReads),
		"the upload body should batch source reads independently of transport read size")
}

// TestClientPushStreamsPrefixBeforeSourceCompletion verifies a demand-driven
// producer can wait for its prefix to reach the transport before yielding more.
func TestClientPushStreamsPrefixBeforeSourceCompletion(t *testing.T) {
	const (
		prefix = "stream this prefix"
		suffix = " before asking for the remainder"
	)
	content := prefix + suffix
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	tc := newTestContext(t)
	sourceReader, sourceWriter := io.Pipe()
	prefixRead := make(chan struct{})
	producerResult := make(chan error, 1)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"streaming"), nil).Once()
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			gotPrefix := make([]byte, len(prefix))
			if _, err := io.ReadFull(req.Body, gotPrefix); err != nil {
				return nil, err
			}
			assert.Equal(t, prefix, string(gotPrefix))
			close(prefixRead)
			gotSuffix, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			if err := req.Body.Close(); err != nil {
				return nil, err
			}
			assert.Equal(t, suffix, string(gotSuffix))
			return response(http.StatusCreated, ""), nil
		}).Once()
	go func() {
		if _, err := io.WriteString(sourceWriter, prefix); err != nil {
			producerResult <- err
			return
		}
		select {
		case <-prefixRead:
		case <-t.Context().Done():
			producerResult <- t.Context().Err()
			return
		}
		if _, err := io.WriteString(sourceWriter, suffix); err != nil {
			producerResult <- err
			return
		}
		producerResult <- sourceWriter.Close()
	}()
	pushResult := make(chan error, 1)
	go func() {
		pushResult <- tc.client.Push(
			t.Context(), repo, digest.FromString(content), int64(len(content)), sourceReader)
	}()

	select {
	case err := <-pushResult:
		require.NoError(t, err)
	case <-time.After(time.Second):
		_ = sourceWriter.CloseWithError(io.ErrClosedPipe)
		_ = sourceReader.CloseWithError(io.ErrClosedPipe)
		t.Fatal("Push did not publish the prefix before waiting for more source data")
	}
	require.NoError(t, <-producerResult)
}

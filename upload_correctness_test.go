package blob_test

import (
	"crypto/sha512"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// seekFailureReader is readable but refuses to report its initial position.
type seekFailureReader struct {
	// reader supplies bytes if the upload reaches the body.
	reader io.Reader
	// err is the stable Seek failure callers should receive.
	err error
}

// emptyBeforeEOFReader injects one valid zero-byte, nil-error Read immediately
// before its underlying reader's EOF.
type emptyBeforeEOFReader struct {
	// reader supplies the declared content.
	reader io.Reader
	// injected records whether the empty Read has already been returned.
	injected bool
}

// Read delegates until EOF, replacing the first empty EOF with (0, nil).
func (r *emptyBeforeEOFReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n == 0 && errors.Is(err, io.EOF) && !r.injected {
		r.injected = true
		return 0, nil
	}
	return n, err
}

// noProgressReader never yields bytes or a terminal error.
type noProgressReader struct{}

// Read reports no progress to exercise the client's bounded tolerance.
func (noProgressReader) Read([]byte) (int, error) {
	return 0, nil
}

// invalidCountReader violates [io.Reader]'s returned-byte-count contract.
type invalidCountReader struct {
	// count is the invalid byte count returned from every Read.
	count int
}

// Read returns the configured invalid count without touching p.
func (r invalidCountReader) Read([]byte) (int, error) {
	return r.count, nil
}

// Read forwards to the underlying source.
func (r *seekFailureReader) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

// Seek returns the configured failure.
func (r *seekFailureReader) Seek(int64, int) (int64, error) {
	return 0, r.err
}

// TestClientPushEnforcesExactReaderSize proves both sides of the declared-size
// contract without buffering the upload.
func TestClientPushEnforcesExactReaderSize(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	t.Run("rejects trailing data after sending only the declared prefix", func(t *testing.T) {
		const content = "prefix-TRAILING"
		const declared = int64(len("prefix"))
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"too-long"), nil).Once()
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)
		expectDelete(tc, uploadEndpoint+"too-long")

		err := tc.client.Push(t.Context(), repo, digest.FromString(content), declared,
			strings.NewReader(content))

		require.ErrorContains(t, err, "source contains data after 6 bytes")
		assert.Equal(t, "prefix", put.body, "the request must never exceed Content-Length")
	})

	t.Run("rejects a source shorter than the declared size", func(t *testing.T) {
		const content = "short"
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"too-short"), nil).Once()
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)
		expectDelete(tc, uploadEndpoint+"too-short")

		err := tc.client.Push(t.Context(), repo, digest.FromString(content), int64(len(content)+1),
			strings.NewReader(content))

		require.ErrorContains(t, err, "yielded 5 bytes, expected 6")
	})

	t.Run("rejects nonempty data declared as zero before opening a session", func(t *testing.T) {
		tc := newTestContext(t)

		err := tc.client.Push(t.Context(), repo, digest.FromString("x"), 0, strings.NewReader("x"))

		require.ErrorContains(t, err, "source contains data after 0 bytes")
	})

	t.Run("sends an empty upload as http.NoBody with Content-Length zero", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"empty"), nil).Once()
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)

		err := tc.client.Push(t.Context(), repo, digest.FromString(""), 0, strings.NewReader(""))

		require.NoError(t, err)
		assert.True(t, put.noBody)
		assert.Zero(t, put.contentLength)
		assert.Empty(t, put.body)
	})
}

// TestClientPushToleratesOneEmptyReadBeforeEOF verifies exact-size validation
// follows the [io.Reader] contract instead of treating one (0, nil) as failure.
func TestClientPushToleratesOneEmptyReadBeforeEOF(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	tests := []struct {
		name    string
		content string
	}{
		{name: "nonempty body", content: "exact bytes"},
		{name: "empty body", content: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t)
			tc.transport.EXPECT().
				RoundTrip(postRequestFor(uploadEndpoint)).
				Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"empty-read"), nil).Once()
			var put capturedPut
			expectPut(tc, &put, http.StatusCreated)
			source := &emptyBeforeEOFReader{reader: strings.NewReader(tt.content)}

			err := tc.client.Push(
				t.Context(), repo, digest.FromString(tt.content), int64(len(tt.content)), source)

			require.NoError(t, err)
			assert.Equal(t, tt.content, put.body)
		})
	}
}

// TestClientPushBoundsRepeatedEmptyReads verifies a broken reader cannot spin
// forever while exact-size validation searches for EOF.
func TestClientPushBoundsRepeatedEmptyReads(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	t.Run("zero-size EOF probe", func(t *testing.T) {
		tc := newTestContext(t)

		err := tc.client.Push(t.Context(), repo, digest.FromString(""), 0, noProgressReader{})

		require.ErrorIs(t, err, io.ErrNoProgress)
	})

	t.Run("active request body", func(t *testing.T) {
		tc := newTestContext(t)
		sessionURL := uploadEndpoint + "no-progress"
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)
		expectDelete(tc, sessionURL)

		err := tc.client.Push(t.Context(), repo, digest.FromString("x"), 1, noProgressReader{})

		require.ErrorIs(t, err, io.ErrNoProgress)
	})
}

// TestClientPushRejectsInvalidReaderCounts verifies a broken caller reader
// produces a normal error rather than panicking the streaming copy loop.
func TestClientPushRejectsInvalidReaderCounts(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	tests := []struct {
		name  string
		count int
	}{
		{name: "negative", count: -1},
		{name: "larger than request slice", count: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t)
			sessionURL := uploadEndpoint + "invalid-count"
			tc.transport.EXPECT().
				RoundTrip(postRequestFor(uploadEndpoint)).
				Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
			var put capturedPut
			expectPut(tc, &put, http.StatusCreated)
			expectDelete(tc, sessionURL)

			err := tc.client.Push(
				t.Context(), repo, digest.FromString("x"), 1, invalidCountReader{count: tt.count})

			require.ErrorContains(t, err, "reader returned invalid byte count")
		})
	}
}

// TestClientPushPreservesOpaqueSessionQuery verifies that adding the digest
// does not parse, reorder, or discard registry-owned session state.
func TestClientPushPreservesOpaqueSessionQuery(t *testing.T) {
	const content = "opaque state"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	location := uploadEndpoint +
		"session?z=last&_state=a;b&a=first&digest=old%3Avalue&unchanged=&d%69gest=duplicate"
	tc := newTestContext(t)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, location), nil).Once()
	var put capturedPut
	expectPut(tc, &put, http.StatusCreated)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.NoError(t, err)
	assert.Equal(t,
		uploadEndpoint+"session?z=last&_state=a;b&a=first&digest=sha256%3A"+dgst.Encoded()+
			"&unchanged=&d%69gest=sha256%3A"+dgst.Encoded(),
		put.url)
}

// TestClientPushResolvesSessionLocationFromRedirectTarget verifies that a
// relative Location belongs to the final POST URL, not the pre-redirect URL.
func TestClientPushResolvesSessionLocationFromRedirectTarget(t *testing.T) {
	const content = "redirected session"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	redirectEndpoint := "https://registry.example.com/redirected/uploads/"
	tc := newTestContext(t)
	redirect := response(http.StatusTemporaryRedirect, "")
	redirect.Header.Set("Location", redirectEndpoint)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(redirect, nil).Once()
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(redirectEndpoint)).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			resp := sessionResponse(http.StatusAccepted, "session")
			resp.Request = req
			return resp, nil
		}).Once()
	var put capturedPut
	expectPut(tc, &put, http.StatusCreated)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.NoError(t, err)
	assert.Equal(t,
		redirectEndpoint+"session?digest=sha256%3A"+dgst.Encoded(), put.url)
}

// TestClientPushRejectsOffSpecCommitAndCancelsSession verifies that 202 is not
// mistaken for a completed OCI commit and the still-open session is deleted.
func TestClientPushRejectsOffSpecCommitAndCancelsSession(t *testing.T) {
	const content = "not committed yet"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	location := uploadEndpoint + "pending?_state=opaque"
	replacement := uploadEndpoint + "replacement?_state=new"
	tc := newTestContext(t)
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, location), nil).Once()
	var put capturedPut
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			body, err := readAndCloseRequestBody(req)
			put = capturedPut{url: req.URL.String(), body: string(body)}
			resp := sessionResponse(http.StatusAccepted, "replacement?_state=new")
			resp.Request = req
			return resp, err
		}).Once()
	expectDelete(tc, replacement)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.ErrorContains(t, err, "registry returned 202")
	assert.Equal(t, location+"&digest=sha256%3A"+dgst.Encoded(), put.url)
}

// TestClientPushReportsUnusableAcceptedSessionLocation verifies that cleanup
// falls back to the prior session and the missing or malformed replacement is visible.
func TestClientPushReportsUnusableAcceptedSessionLocation(t *testing.T) {
	const content = "invalid replacement"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	tests := []struct {
		name     string
		location string
		wantErr  string
	}{
		{name: "reports a malformed Location", location: "%", wantErr: "unparseable Location"},
		{name: "reports a missing Location", wantErr: "no Location header"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionURL := uploadEndpoint + "original"
			tc := newTestContext(t)
			tc.transport.EXPECT().
				RoundTrip(postRequestFor(uploadEndpoint)).
				Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
			tc.transport.EXPECT().
				RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
					return req.Method == http.MethodPut
				})).
				RunAndReturn(func(req *http.Request) (*http.Response, error) {
					_, err := readAndCloseRequestBody(req)
					resp := sessionResponse(http.StatusAccepted, tt.location)
					resp.Request = req
					return resp, err
				}).Once()
			expectDelete(tc, sessionURL)

			err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

			require.ErrorContains(t, err, "registry returned 202")
			require.ErrorContains(t, err, "could not resolve upload-session Location for cleanup")
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestClientPushPreservesEarlyRegistryStatus verifies that an error response
// may arrive before a registry consumes the request body without being
// misreported as a reader-size mismatch.
func TestClientPushPreservesEarlyRegistryStatus(t *testing.T) {
	const content = "body not consumed"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	t.Run("monolithic PUT preserves 503", func(t *testing.T) {
		tc := newTestContext(t)
		sessionURL := uploadEndpoint + "early-put"
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		failed := response(http.StatusServiceUnavailable, "")
		failed.Header.Set("Location", "https://unrelated.example/delete-me")
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPut
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				_ = req.Body.Close()
				return failed, nil
			}).Once()
		expectDelete(tc, sessionURL)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.ErrorContains(t, err, "registry returned 503")
		assert.NotContains(t, err.Error(), "reader size")
	})

	t.Run("chunk PATCH preserves 503", func(t *testing.T) {
		tc := newTestContext(t, blob.WithChunkedUpload(int64(len(content))))
		sessionURL := uploadEndpoint + "early-patch"
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPatch
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				_ = req.Body.Close()
				return response(http.StatusServiceUnavailable, ""), nil
			}).Once()
		expectDelete(tc, sessionURL)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.ErrorContains(t, err, "registry returned 503")
		assert.NotContains(t, err.Error(), "reader size")
	})
}

// TestClientPushPrioritizesProvenSourceErrors verifies that a registry status
// cannot mask an exact-size failure the transport already observed.
func TestClientPushPrioritizesProvenSourceErrors(t *testing.T) {
	const content = "short"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	t.Run("monolithic PUT", func(t *testing.T) {
		tc := newTestContext(t)
		sessionURL := uploadEndpoint + "short-put"
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPut
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				_, _ = readAndCloseRequestBody(req)
				return response(http.StatusServiceUnavailable, ""), nil
			}).Once()
		expectDelete(tc, sessionURL)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)+1), strings.NewReader(content))

		require.ErrorContains(t, err, "yielded 5 bytes, expected 6")
		assert.NotContains(t, err.Error(), "registry returned 503")
	})

	t.Run("chunk PATCH", func(t *testing.T) {
		tc := newTestContext(t, blob.WithChunkedUpload(int64(len(content)+1)))
		sessionURL := uploadEndpoint + "short-patch"
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPatch
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				_, _ = readAndCloseRequestBody(req)
				return response(http.StatusServiceUnavailable, ""), nil
			}).Once()
		expectDelete(tc, sessionURL)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)+1), strings.NewReader(content))

		require.ErrorContains(t, err, "yielded 5 bytes, expected 6")
		assert.NotContains(t, err.Error(), "registry returned 503")
	})
}

// TestClientPushRejectsSuccessfulResponsesThatDidNotConsumeTheBody verifies a
// registry cannot acknowledge bytes its transport never read from the request.
func TestClientPushRejectsSuccessfulResponsesThatDidNotConsumeTheBody(t *testing.T) {
	const content = "transport must consume all of this"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	t.Run("monolithic PUT", func(t *testing.T) {
		tc := newTestContext(t)
		sessionURL := uploadEndpoint + "partial-consume-put"
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPut
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				var first [1]byte
				_, _ = req.Body.Read(first[:])
				_ = req.Body.Close()
				return response(http.StatusCreated, ""), nil
			}).Once()
		expectDelete(tc, sessionURL)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.ErrorContains(t, err, "request consumed 1 bytes")
		require.ErrorContains(t, err, fmt.Sprintf("expected %d", len(content)))
	})

	t.Run("chunk PATCH", func(t *testing.T) {
		tc := newTestContext(t, blob.WithChunkedUpload(int64(len(content))))
		sessionURL := uploadEndpoint + "partial-consume-patch"
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPatch
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				var first [1]byte
				_, _ = req.Body.Read(first[:])
				_ = req.Body.Close()
				resp := response(http.StatusAccepted, "")
				resp.Header.Set("Range", fmt.Sprintf("0-%d", len(content)-1))
				return resp, nil
			}).Once()
		expectDelete(tc, sessionURL)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.ErrorContains(t, err, "request consumed 1 bytes")
		require.ErrorContains(t, err, fmt.Sprintf("expected %d", len(content)))
	})
}

// TestClientPushRedirectedChunkRetainsLogicalOffset verifies replayed chunks
// report exact-size failures in blob coordinates rather than chunk coordinates.
func TestClientPushRedirectedChunkRetainsLogicalOffset(t *testing.T) {
	const content = "123456789"
	const declaredSize = int64(10)
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString(content)
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	sessionURL := uploadEndpoint + "offset"
	redirectURL := "https://registry.example.com/offset-final"
	tc := newTestContext(t, blob.WithChunkedUpload(8))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint)).
		Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
	var first capturedPatch
	expectPatch(tc, &first, http.StatusAccepted, "0-7", "")
	redirect := response(http.StatusTemporaryRedirect, "")
	redirect.Header.Set("Location", redirectURL)
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPatch && req.URL.String() == sessionURL
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_ = req.Body.Close()
			return redirect, nil
		}).Once()
	tc.transport.EXPECT().
		RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
			return req.Method == http.MethodPatch && req.URL.String() == redirectURL
		})).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			_, _ = readAndCloseRequestBody(req)
			resp := response(http.StatusAccepted, "")
			resp.Header.Set("Range", "0-9")
			return resp, nil
		}).Once()
	expectDelete(tc, sessionURL)

	err := tc.client.Push(t.Context(), repo, dgst, declaredSize, strings.NewReader(content))

	require.ErrorContains(t, err, "yielded 9 bytes, expected 10")
}

// TestClientPushHandlesNilAndSeekFailures checks public input safety before
// any registry request is attempted.
func TestClientPushHandlesNilAndSeekFailures(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("content")

	t.Run("rejects a nil context without panicking", func(t *testing.T) {
		tc := newTestContext(t)

		err := tc.client.Push(
			nil, //nolint:staticcheck // Intentionally verifies invalid nil input.
			repo, dgst, 0, strings.NewReader(""))

		require.EqualError(t, err, "nil context")
	})

	t.Run("rejects a typed-nil reader without panicking", func(t *testing.T) {
		tc := newTestContext(t)
		var reader *strings.Reader

		assert.NotPanics(t, func() {
			err := tc.client.Push(t.Context(), repo, dgst, 7, reader)
			require.ErrorContains(t, err, "nil reader")
		})
	})

	t.Run("ignores a nil transfer option", func(t *testing.T) {
		const content = "content"
		tc := newTestContext(t)
		uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"nil-option"), nil).Once()
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)
		var option blob.TransferOption

		err := tc.client.Push(t.Context(), repo, digest.FromString(content), int64(len(content)),
			strings.NewReader(content), option)

		require.NoError(t, err)
	})

	t.Run("preserves an initial Seek error", func(t *testing.T) {
		tc := newTestContext(t)
		seekErr := errors.New("seek device failed")
		reader := &seekFailureReader{reader: strings.NewReader("content"), err: seekErr}

		err := tc.client.Push(t.Context(), repo, dgst, 7, reader)

		require.ErrorIs(t, err, seekErr)
		require.ErrorContains(t, err, "capturing reader position")
	})
}

// TestClientPushChunkedEnforcesProtocolAndSize exercises failures unique to
// the PATCH upload path.
func TestClientPushChunkedEnforcesProtocolAndSize(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	t.Run("rejects trailing reader bytes before the final commit", func(t *testing.T) {
		const content = "prefix-TRAILING"
		tc := newTestContext(t, blob.WithChunkedUpload(3))
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"chunk-trailing"), nil).Once()
		var first, second capturedPatch
		expectPatch(tc, &first, http.StatusAccepted, "0-2", "")
		expectPatch(tc, &second, http.StatusAccepted, "0-5", "")
		expectDelete(tc, uploadEndpoint+"chunk-trailing")

		err := tc.client.Push(t.Context(), repo, digest.FromString(content), 6, strings.NewReader(content))

		require.ErrorContains(t, err, "source contains data after 6 bytes")
		assert.Equal(t, "pre", first.body)
		assert.Equal(t, "fix", second.body)
	})

	t.Run("rejects a reader shorter than the declared chunks", func(t *testing.T) {
		const content = "short"
		tc := newTestContext(t, blob.WithChunkedUpload(6))
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"chunk-short"), nil).Once()
		var patch capturedPatch
		expectPatch(tc, &patch, http.StatusAccepted, "0-5", "")
		expectDelete(tc, uploadEndpoint+"chunk-short")

		err := tc.client.Push(t.Context(), repo, digest.FromString(content), 6, strings.NewReader(content))

		require.ErrorContains(t, err, "yielded 5 bytes, expected 6")
	})

	t.Run("rejects an off-spec PATCH status", func(t *testing.T) {
		const content = "abc"
		tc := newTestContext(t, blob.WithChunkedUpload(3))
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"patch-200"), nil).Once()
		var patch capturedPatch
		expectPatch(tc, &patch, http.StatusOK, "0-2", "")
		expectDelete(tc, uploadEndpoint+"patch-200")

		err := tc.client.Push(t.Context(), repo, digest.FromString(content), 3, strings.NewReader(content))

		require.ErrorContains(t, err, "registry returned 200")
	})

	t.Run("rejects an off-spec final commit status", func(t *testing.T) {
		const content = "abc"
		tc := newTestContext(t, blob.WithChunkedUpload(3))
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"commit-202"), nil).Once()
		var patch capturedPatch
		expectPatch(tc, &patch, http.StatusAccepted, "0-2", "")
		var put capturedPut
		expectPut(tc, &put, http.StatusAccepted)
		expectDelete(tc, uploadEndpoint+"commit-202")

		err := tc.client.Push(t.Context(), repo, digest.FromString(content), 3, strings.NewReader(content))

		require.ErrorContains(t, err, "registry returned 202")
	})
}

// TestClientPushChunkedAdvertisesNonSHA256Digest verifies the OCI session
// negotiation query for a chunked upload using another digest algorithm.
func TestClientPushChunkedAdvertisesNonSHA256Digest(t *testing.T) {
	const content = "sha512 content"
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	sum := sha512.Sum512([]byte(content))
	dgst := digest.NewDigestFromBytes(digest.SHA512, sum[:])
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"
	tc := newTestContext(t, blob.WithChunkedUpload(int64(len(content))))
	tc.transport.EXPECT().
		RoundTrip(postRequestFor(uploadEndpoint+"?digest-algorithm=sha512")).
		Return(sessionResponse(http.StatusAccepted, uploadEndpoint+"sha512"), nil).Once()
	var patch capturedPatch
	expectPatch(tc, &patch, http.StatusAccepted, "0-13", "")
	var put capturedPut
	expectPut(tc, &put, http.StatusCreated)

	err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

	require.NoError(t, err)
}

// TestClientPushReplaysWriteRedirectBodies verifies that a 307/308 response
// can arrive before the first transport consumes the body and validation then
// follows the replay body that reached the final target.
func TestClientPushReplaysWriteRedirectBodies(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	uploadEndpoint := "https://registry.example.com/v2/library/ubuntu/blobs/uploads/"

	t.Run("replays a monolithic PUT", func(t *testing.T) {
		const content = "redirected monolithic body"
		dgst := digest.FromString(content)
		sessionURL := uploadEndpoint + "redirect-put"
		commitURL := sessionURL + "?digest=sha256%3A" + dgst.Encoded()
		redirectURL := "https://registry.example.com/upload-target"
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		redirect := response(http.StatusTemporaryRedirect, "")
		redirect.Header.Set("Location", redirectURL)
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPut && req.URL.String() == commitURL
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				_ = req.Body.Close()
				return redirect, nil
			}).Once()
		var replayed string
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPut && req.URL.String() == redirectURL
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				body, err := readAndCloseRequestBody(req)
				replayed = string(body)
				return response(http.StatusCreated, ""), err
			}).Once()

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.NoError(t, err)
		assert.Equal(t, content, replayed)
	})

	t.Run("replays a chunk PATCH", func(t *testing.T) {
		const content = "redirected chunk"
		dgst := digest.FromString(content)
		sessionURL := uploadEndpoint + "redirect-patch"
		redirectURL := "https://registry.example.com/chunk-target"
		tc := newTestContext(t, blob.WithChunkedUpload(int64(len(content))))
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		redirect := response(http.StatusPermanentRedirect, "")
		redirect.Header.Set("Location", redirectURL)
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPatch && req.URL.String() == sessionURL
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				_ = req.Body.Close()
				return redirect, nil
			}).Once()
		var replayed string
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPatch && req.URL.String() == redirectURL
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				body, err := readAndCloseRequestBody(req)
				resp := response(http.StatusAccepted, "")
				resp.Header.Set("Range", "0-15")
				resp.Header.Set("Location", "next-session")
				resp.Request = req
				replayed = string(body)
				return resp, err
			}).Once()
		var put capturedPut
		expectPut(tc, &put, http.StatusCreated)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)), strings.NewReader(content))

		require.NoError(t, err)
		assert.Equal(t, content, replayed)
		assert.Equal(t,
			"https://registry.example.com/next-session?digest=sha256%3A"+dgst.Encoded(),
			put.url, "relative PATCH Location should resolve from the redirect target")
	})

	t.Run("explains when a non-seekable body cannot be replayed", func(t *testing.T) {
		const content = "non-seekable redirect"
		dgst := digest.FromString(content)
		sessionURL := uploadEndpoint + "non-seekable"
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(postRequestFor(uploadEndpoint)).
			Return(sessionResponse(http.StatusAccepted, sessionURL), nil).Once()
		redirect := response(http.StatusTemporaryRedirect, "")
		redirect.Header.Set("Location", "https://registry.example.com/unreachable-replay")
		tc.transport.EXPECT().
			RoundTrip(mock.MatchedBy(func(req *http.Request) bool {
				return req.Method == http.MethodPut
			})).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				_ = req.Body.Close()
				return redirect, nil
			}).Once()
		expectDelete(tc, sessionURL)

		err := tc.client.Push(t.Context(), repo, dgst, int64(len(content)),
			iotest.OneByteReader(strings.NewReader(content)))

		require.ErrorContains(t, err, "reader is not an io.Seeker")
		require.ErrorContains(t, err, "cannot be replayed")
	})
}

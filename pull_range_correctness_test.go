package blob_test

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blob "github.com/imgoci/go-oci-blob"
)

// partialRangeResponse builds a partial response with an explicit
// Content-Range value so tests can exercise invalid registry answers.
func partialRangeResponse(body, contentRange string) *http.Response {
	resp := response(http.StatusPartialContent, body)
	resp.Header.Set("Content-Range", contentRange)
	return resp
}

func TestPullRangeValidatesPartialResponse(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	tests := []struct {
		name         string
		contentRange string
		wantErr      string
	}{
		{name: "requires Content-Range", contentRange: "", wantErr: "invalid Content-Range"},
		{name: "requires the requested start", contentRange: "bytes 0-4/20", wantErr: "instead of requested byte 5"},
		{
			name:         "rejects bytes beyond the requested end",
			contentRange: "bytes 5-10/20",
			wantErr:      "beyond requested end byte 9",
		},
		{name: "rejects an interval outside the blob", contentRange: "bytes 5-9/9", wantErr: "invalid Content-Range"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t)
			tc.transport.EXPECT().
				RoundTrip(getRequestFor(endpoint, "bytes=5-9")).
				Return(partialRangeResponse("wrong", tt.contentRange), nil).Once()

			rc, err := tc.client.PullRange(t.Context(), repo, dgst, 5, 5)

			require.ErrorContains(t, err, tt.wantErr)
			assert.Nil(t, rc)
		})
	}
}

func TestPullRangeDeliversExactlyTheRequestedWindow(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	t.Run("caps a response body at its advertised interval", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=5-9")).
			Return(partialRangeResponse(content[5:10]+"WRONG", "bytes 5-9/20"), nil).Once()

		rc, err := tc.client.PullRange(t.Context(), repo, dgst, 5, 5)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, content[5:10], string(got))
	})

	t.Run("continues a compliant shorter partial response", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=5-9")).
			Return(partialRangeResponse(content[5:8], "bytes 5-7/20"), nil).Once()
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=8-9")).
			Return(partialRangeResponse(content[8:10], "bytes 8-9/20"), nil).Once()

		rc, err := tc.client.PullRange(t.Context(), repo, dgst, 5, 5)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, content[5:10], string(got))
	})

	t.Run("reports a partial body shorter than Content-Range", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=5-9")).
			Return(partialRangeResponse(content[5:8], "bytes 5-9/20"), nil).Once()

		rc, err := tc.client.PullRange(t.Context(), repo, dgst, 5, 5)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.NoError(t, rc.Close())
		assert.Equal(t, content[5:8], string(got))
	})

	t.Run("reports a requested window extending past the blob", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=18-22")).
			Return(partialRangeResponse(content[18:20], "bytes 18-19/20"), nil).Once()

		rc, err := tc.client.PullRange(t.Context(), repo, dgst, 18, 5)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.NoError(t, rc.Close())
		assert.Equal(t, content[18:], string(got))
	})
}

func TestPullRangeRejectsUnrepresentableAndPastEndWindows(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefghij"
	dgst := digest.FromString(content)
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	t.Run("rejects arithmetic overflow before a request", func(t *testing.T) {
		tc := newTestContext(t)

		rc, err := tc.client.PullRange(t.Context(), repo, dgst, math.MaxInt64, 2)

		require.ErrorContains(t, err, "overflow")
		assert.Nil(t, rc)
	})

	t.Run("rejects an ignored range starting exactly at EOF", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=20-24")).
			Return(response(http.StatusOK, content), nil).Once()

		rc, err := tc.client.PullRange(t.Context(), repo, dgst, 20, 5)

		require.ErrorContains(t, err, "no data at range offset 20")
		assert.Nil(t, rc)
	})

	t.Run("reports an ignored-range window extending past EOF", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=18-22")).
			Return(response(http.StatusOK, content), nil).Once()

		rc, err := tc.client.PullRange(t.Context(), repo, dgst, 18, 5)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.NoError(t, rc.Close())
		assert.Equal(t, content[18:], string(got))
	})
}

func TestPullRangeIgnoredRangePreservesContextCancellation(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	dgst := digest.FromString("context-bound range")
	endpoint := "https://registry.example.com/v2/library/ubuntu/blobs/" + dgst.String()

	tests := []struct {
		name   string
		offset int64
	}{
		{name: "while discarding the prefix", offset: 1},
		{name: "while checking the first byte", offset: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			tc := newTestContext(t)
			resp := response(http.StatusOK, "")
			resp.Body = io.NopCloser(readerFunc(func([]byte) (int, error) {
				cancel()
				return 0, errors.New("stale connection reset")
			}))
			rangeHeader := "bytes=0-0"
			if tt.offset > 0 {
				rangeHeader = "bytes=1-1"
			}
			tc.transport.EXPECT().
				RoundTrip(getRequestFor(endpoint, rangeHeader)).
				Return(resp, nil).Once()

			rc, err := tc.client.PullRange(ctx, repo, dgst, tt.offset, 1)

			require.ErrorIs(t, err, context.Canceled)
			assert.Nil(t, rc)
		})
	}
}

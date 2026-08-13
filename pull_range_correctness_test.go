package blob_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"testing"
	"time"

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
	}{
		{name: "requires Content-Range", contentRange: ""},
		{name: "requires the requested start", contentRange: "bytes 0-4/20"},
		{
			name:         "rejects bytes beyond the requested end",
			contentRange: "bytes 5-10/20",
		},
		{name: "rejects an interval outside the blob", contentRange: "bytes 5-9/9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestContext(t)
			tc.transport.EXPECT().
				RoundTrip(getRequestFor(endpoint, "bytes=5-9")).
				Return(partialRangeResponse("wrong", tt.contentRange), nil).Once()

			rc, err := tc.client.PullRange(t.Context(), repo, dgst, 5, 5)

			require.Error(t, err)
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

		require.Error(t, err)
		assert.Nil(t, rc)
	})

	t.Run("rejects an ignored range starting exactly at EOF", func(t *testing.T) {
		tc := newTestContext(t)
		tc.transport.EXPECT().
			RoundTrip(getRequestFor(endpoint, "bytes=20-24")).
			Return(response(http.StatusOK, content), nil).Once()

		rc, err := tc.client.PullRange(t.Context(), repo, dgst, 20, 5)

		require.ErrorIs(t, err, io.EOF)
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

// TestPullRangeBoundsShortPartialResponses verifies successful fragments are
// bounded independently from each fragment's retry budget.
func TestPullRangeBoundsShortPartialResponses(t *testing.T) {
	repo := blob.Repository{Host: "registry.example.com", Name: "library/ubuntu"}
	content := "0123456789abcdefg"
	dgst := digest.FromString(content)

	tests := []struct {
		name              string
		length            int64
		maxAttempts       int
		retryContinuation bool
		wantCalls         int
		wantData          string
		wantError         bool
	}{
		{
			name:        "accepts sixteen successful portions",
			length:      16,
			maxAttempts: 1,
			wantCalls:   16,
			wantData:    content[:16],
		},
		{
			name:        "rejects a seventeenth successful portion",
			length:      17,
			maxAttempts: 1,
			wantCalls:   16,
			wantData:    content[:16],
			wantError:   true,
		},
		{
			name:              "retains retries for an allowed continuation",
			length:            2,
			maxAttempts:       2,
			retryContinuation: true,
			wantCalls:         3,
			wantData:          content[:2],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				var start, end int64
				n, err := fmt.Sscanf(req.Header.Get("Range"), "bytes=%d-%d", &start, &end)
				require.NoError(t, err)
				require.Equal(t, 2, n)
				require.LessOrEqual(t, start, end)
				if tt.retryContinuation && calls == 2 {
					return response(http.StatusServiceUnavailable, ""), nil
				}
				return partialRangeResponse(
					content[start:start+1],
					fmt.Sprintf("bytes %d-%d/%d", start, start, len(content)),
				), nil
			})
			client := blob.New(
				blob.WithTransport(transport),
				blob.WithRetryPolicy(blob.RetryPolicy{
					MaxAttempts:  tt.maxAttempts,
					InitialDelay: time.Nanosecond,
					MaxDelay:     time.Nanosecond,
				}),
			)

			rc, err := client.PullRange(t.Context(), repo, dgst, 0, tt.length)
			require.NoError(t, err)
			got, err := io.ReadAll(rc)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, rc.Close())
			assert.Equal(t, tt.wantData, string(got))
			assert.Equal(t, tt.wantCalls, calls)
		})
	}
}

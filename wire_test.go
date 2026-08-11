package blob

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlobURL(t *testing.T) {
	dgst := digest.FromString("hello")

	got := blobURL("https", Repository{Host: "registry.example.com:5000", Name: "library/ubuntu"}, dgst)

	assert.Equal(t,
		"https://registry.example.com:5000/v2/library/ubuntu/blobs/"+dgst.String(),
		got.String())
}

func TestResolveLocation(t *testing.T) {
	base, err := url.Parse("https://registry.example.com/v2/library/ubuntu/blobs/uploads/")
	require.NoError(t, err)

	tests := []struct {
		name     string
		location string
		want     string
		wantErr  string
	}{
		{
			name:     "resolves an absolute URL as itself",
			location: "https://cdn.example.com/upload/abc",
			want:     "https://cdn.example.com/upload/abc",
		},
		{
			name:     "resolves an absolute path against the base host",
			location: "/v2/library/ubuntu/blobs/uploads/abc?state=x",
			want:     "https://registry.example.com/v2/library/ubuntu/blobs/uploads/abc?state=x",
		},
		{
			name:     "resolves a relative path against the base path",
			location: "abc",
			want:     "https://registry.example.com/v2/library/ubuntu/blobs/uploads/abc",
		},
		{
			name:     "rejects an empty location",
			location: "",
			wantErr:  "no Location header",
		},
		{
			name:     "rejects an unparseable location",
			location: "http://registry.example.com/%zz",
			wantErr:  "unparseable Location",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveLocation(base, tt.location)

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.String())
		})
	}
}

func TestIsSuccess(t *testing.T) {
	assert.True(t, isSuccess(http.StatusOK), "200 is a success")
	assert.True(t, isSuccess(http.StatusCreated), "201 is a success")
	assert.True(t, isSuccess(http.StatusAccepted), "202 is a success")
	assert.False(t, isSuccess(http.StatusMovedPermanently), "3xx is not a success")
	assert.False(t, isSuccess(http.StatusNotFound), "4xx is not a success")
	assert.False(t, isSuccess(http.StatusInternalServerError), "5xx is not a success")
}

func TestParseErrorBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "renders code and message",
			body: `{"errors":[{"code":"BLOB_UNKNOWN","message":"blob unknown to registry"}]}`,
			want: "BLOB_UNKNOWN: blob unknown to registry",
		},
		{
			name: "joins multiple errors",
			body: `{"errors":[{"code":"A","message":"first"},{"code":"B","message":"second"}]}`,
			want: "A: first; B: second",
		},
		{
			name: "renders a code without a message",
			body: `{"errors":[{"code":"DENIED"}]}`,
			want: "DENIED",
		},
		{
			name: "renders a message without a code",
			body: `{"errors":[{"message":"try again"}]}`,
			want: "try again",
		},
		{
			name: "yields nothing for garbage",
			body: `<html>502 Bad Gateway</html>`,
			want: "",
		},
		{
			name: "yields nothing for an empty body",
			body: ``,
			want: "",
		},
		{
			name: "yields nothing for JSON of another shape",
			body: `{"message":"not the OCI envelope"}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseErrorBody([]byte(tt.body)))
		})
	}
}

func TestInterpretError(t *testing.T) {
	tests := []struct {
		name        string
		resp        *http.Response
		wantMessage string
		wantIs      error
	}{
		{
			name: "carries the parsed OCI detail",
			resp: &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body: io.NopCloser(strings.NewReader(
					`{"errors":[{"code":"UNKNOWN","message":"boom"}]}`)),
			},
			wantMessage: "registry returned 500: UNKNOWN: boom",
		},
		{
			name: "falls back to the status when the body is garbage",
			resp: &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("<html>oops</html>")),
			},
			wantMessage: "registry returned 502 Bad Gateway",
		},
		{
			name: "handles a nil body",
			resp: &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       nil,
			},
			wantMessage: "registry returned 403 Forbidden",
		},
		{
			name: "maps 404 onto ErrNotFound",
			resp: &http.Response{
				StatusCode: http.StatusNotFound,
				Body: io.NopCloser(strings.NewReader(
					`{"errors":[{"code":"BLOB_UNKNOWN","message":"blob unknown to registry"}]}`)),
			},
			wantMessage: "registry returned 404: BLOB_UNKNOWN: blob unknown to registry",
			wantIs:      ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := interpretError(tt.resp)

			require.Error(t, err)
			assert.Equal(t, tt.wantMessage, err.Error())
			if tt.wantIs != nil {
				assert.ErrorIs(t, err, tt.wantIs)
			}
		})
	}
}

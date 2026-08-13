package blob

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveLocation(t *testing.T) {
	base, err := url.Parse("https://registry.example.com/v2/library/ubuntu/blobs/uploads/")
	require.NoError(t, err)

	tests := []struct {
		name      string
		location  string
		want      string
		wantError bool
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
			name:     "drops a fragment without changing opaque query bytes",
			location: "abc?_state=a;b&token=%2Fopaque#peer-fragment",
			want:     "https://registry.example.com/v2/library/ubuntu/blobs/uploads/abc?_state=a;b&token=%2Fopaque",
		},
		{name: "rejects an empty location", wantError: true},
		{
			name:      "rejects an unparseable location",
			location:  "https://storage.example.com/%zz?signature=secret",
			wantError: true,
		},
		{
			name:      "rejects an unsupported absolute scheme",
			location:  "gopher://storage.example.com/upload/abc?signature=secret",
			wantError: true,
		},
		{
			name:      "rejects user information",
			location:  "https://user:password@storage.example.com/upload/abc",
			wantError: true,
		},
		{
			name:      "rejects an HTTPS downgrade",
			location:  "http://storage.example.com/upload/abc?signature=secret",
			wantError: true,
		},
		{
			name:      "rejects a hostless absolute URL",
			location:  "https:///upload/abc?signature=secret",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveLocation(base, tt.location)

			if tt.wantError {
				require.Error(t, err)
				if tt.location != "" {
					assert.NotContains(t, err.Error(), tt.location)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.String())
		})
	}
}

func TestInterpretError(t *testing.T) {
	peerDetail := "reflected-secret\u001b[2J"
	resp := &http.Response{
		StatusCode: http.StatusNotFound,
		Body: io.NopCloser(strings.NewReader(
			`{"errors":[{"code":"BLOB_UNKNOWN","message":"` + peerDetail + `"}]}`)),
		Header: http.Header{},
	}

	err := interpretError(resp)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound)
	assert.Contains(t, err.Error(), "404 Not Found")
	assert.NotContains(t, err.Error(), "BLOB_UNKNOWN")
	assert.NotContains(t, err.Error(), "reflected-secret")
	assert.NotContains(t, err.Error(), "\u001b")
}

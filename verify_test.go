package blob

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyReader(t *testing.T) {
	content := "the quick brown fox jumps over the lazy dog"

	tests := []struct {
		name     string
		body     string
		expected digest.Digest
		wantErr  error
	}{
		{
			name:     "yields EOF when the content matches",
			body:     content,
			expected: digest.FromString(content),
		},
		{
			name:     "yields ErrDigestMismatch on tampered content",
			body:     content + " (tampered)",
			expected: digest.FromString(content),
			wantErr:  ErrDigestMismatch,
		},
		{
			name:     "yields ErrDigestMismatch on truncated content",
			body:     content[:10],
			expected: digest.FromString(content),
			wantErr:  ErrDigestMismatch,
		},
		{
			name:     "verifies an empty blob",
			body:     "",
			expected: digest.FromString(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newVerifyReader(io.NopCloser(strings.NewReader(tt.body)), tt.expected)

			got, err := io.ReadAll(r)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.body, string(got), "verified bytes should pass through unchanged")
		})
	}
}

func TestVerifyReaderHashesAcrossSmallReads(t *testing.T) {
	content := "chunked hashing must accumulate across reads"
	r := newVerifyReader(
		io.NopCloser(iotest.OneByteReader(strings.NewReader(content))),
		digest.FromString(content))

	got, err := io.ReadAll(r)

	require.NoError(t, err)
	assert.Equal(t, content, string(got))
}

func TestVerifyReaderBehavesLikeAReader(t *testing.T) {
	content := "reader contract check"
	r := newVerifyReader(io.NopCloser(strings.NewReader(content)), digest.FromString(content))

	assert.NoError(t, iotest.TestReader(r, []byte(content)))
}

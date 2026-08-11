package blob

import (
	"errors"
	"fmt"
	"io"

	"github.com/opencontainers/go-digest"
)

// verifyReader wraps a blob stream and hashes bytes as they flow. At
// end of stream it swaps [io.EOF] for [ErrDigestMismatch] when the
// content did not hash to the expected digest, so a caller that reads
// to completion has verified the blob without buffering it.
type verifyReader struct {
	// body is the underlying blob stream.
	body io.ReadCloser
	// verifier accumulates the hash of every byte read so far.
	verifier digest.Verifier
	// expected is the digest the full stream must hash to.
	expected digest.Digest
}

// newVerifyReader wraps body in a digest check against expected.
func newVerifyReader(body io.ReadCloser, expected digest.Digest) *verifyReader {
	return &verifyReader{
		body:     body,
		verifier: expected.Verifier(),
		expected: expected,
	}
}

// Read passes bytes through while hashing them. When the stream ends,
// it returns [io.EOF] only if the content verified and
// [ErrDigestMismatch] otherwise.
func (v *verifyReader) Read(p []byte) (int, error) {
	n, err := v.body.Read(p)
	if n > 0 {
		// digest.Verifier is a hash.Hash writer; it cannot fail.
		_, _ = v.verifier.Write(p[:n])
	}
	if errors.Is(err, io.EOF) && !v.verifier.Verified() {
		return n, fmt.Errorf("blob %s: %w", v.expected, ErrDigestMismatch)
	}
	return n, err
}

// Close closes the underlying stream.
func (v *verifyReader) Close() error {
	return v.body.Close()
}

package blob

import "errors"

// ErrNotFound reports that the requested blob or repository does not
// exist on the registry. Operations that treat absence as a normal
// answer, such as [Client.Exists], translate it instead of returning
// it.
var ErrNotFound = errors.New("blob not found")

// ErrDigestMismatch reports that transferred bytes did not hash to
// the expected digest. The verifying reader returned by [Client.Pull]
// yields it in place of [io.EOF] when the stream ends on content that
// fails verification.
var ErrDigestMismatch = errors.New("digest mismatch")

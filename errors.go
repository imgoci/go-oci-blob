package blob

import "errors"

// ErrNotFound reports that the requested blob or repository does not
// exist on the registry. Operations that treat absence as a normal
// answer, such as [Client.Exists], translate it instead of returning
// it.
var ErrNotFound = errors.New("blob not found")

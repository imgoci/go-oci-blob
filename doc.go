// Package blob uploads and downloads OCI blobs.
//
// The library covers exactly the blob subset of the OCI distribution
// spec: push, pull, existence checks, and cross-repository mounts. It
// does not touch manifests, tags, or authentication; callers inject an
// authenticated http.RoundTripper.
//
// The design is documented in docs/docs/explanation/design.md.
package blob

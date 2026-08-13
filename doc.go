// Package blob transfers blobs to and from OCI registries.
//
// The package covers the blob subset of the OCI distribution specification:
// push, pull, existence checks, and cross-repository mounts. It does not manage
// manifests, tags, authentication, credentials, or destination policy.
//
// A [Client] routes registry-origin requests through the transport supplied to
// [WithTransport]. Callers must supply an authenticated transport when the
// registry requires credentials. Requests to registry-selected off-origin
// storage use [WithStorageTransport] instead. The caller remains responsible
// for deciding which storage destinations that transport may reach.
//
// # Embedding with an outer retry loop
//
// [RetryPolicy] has a zero-value one-attempt mode. This lets an embedding
// orchestrator own its operation-level retry budget without nested retries:
//
//	client := blob.New(
//		blob.WithTransport(authenticatedRegistryTransport),
//		blob.WithStorageTransport(guardedStorageTransport),
//		blob.WithRetryPolicy(blob.RetryPolicy{}),
//		blob.WithWriteRedirects(false),
//	)
//
//	err := client.Push(
//		ctx, repo, dgst, size, body,
//		blob.WithWireProgress(reportWireBytes),
//	)
//
// After an operation fails, [Retryable] reports whether a fresh operation may
// succeed and returns the usable Retry-After floor, if one was supplied.
// [StatusCode] exposes a retained HTTP response status. Errors can also be
// inspected with [errors.Is] for [ErrNotFound], [ErrUnauthorized],
// [ErrTooLarge], and [ErrDigestMismatch]. These inspection APIs survive
// contextual wrapping; callers do not need to parse rendered error text.
//
// # Progress
//
// [WithProgress] reports cumulative committed transfer progress.
// [WithWireProgress] reports positive upload-byte deltas as the HTTP transport
// consumes request bodies, including bytes consumed by failed attempts,
// redirects, and transparent retries. Source read-ahead does not count as wire
// progress.
//
// # Redirects
//
// By default, the client follows method-preserving write redirects to preserve
// v1 behavior. [WithWriteRedirects] can reject redirects that would reissue
// POST, PUT, PATCH, or DELETE. This option does not reject upload-session
// Location values returned by successful registry responses.
//
// The design is documented in docs/docs/explanation/design.md.
package blob

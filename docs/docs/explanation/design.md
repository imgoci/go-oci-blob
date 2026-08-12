# Design

go-oci-blob is a Go library that uploads and downloads OCI blobs. That is the
whole library. This page records the design before implementation and the
reasoning behind it.

## Scope

In scope:

- Upload (push) a blob to an OCI registry.
- Download (pull) a blob from an OCI registry.
- Check that a blob exists.
- Cross-repository blob mount, with fallback to a normal push.
- Retries, resume, and digest verification for all of the above.

Out of scope:

- Manifests, tags, referrers, and the rest of the OCI distribution spec.
- Authentication. The caller injects an authenticated `http.RoundTripper`.
  Libraries such as `oras-go` and `go-containerregistry` already provide them.
- Signatures, attestations, and image tooling of any kind.

Every line of code must serve blob transfer. When a feature request falls
outside that sentence, the answer is no.

## Dependencies

The runtime dependency list is the Go standard library plus
`github.com/opencontainers/go-digest`.

`go-digest` is included for interoperability, not function. The libraries that
callers pair with this one already use `digest.Digest`, so our API accepts it
directly. The alternative was a local `~50` line digest type; it would work but
would force every caller to convert strings at the boundary.

Test-only dependencies (mockery, testify, testcontainers) do not ship to
consumers.

## Architecture

The library follows the repository's hexagonal rules (A1). The split:

- **Core (pure logic, no I/O):** request planning, response interpretation,
  retry decisions, chunking, and digest bookkeeping. This code runs and tests
  without a network.
- **Port:** a single interface over one HTTP round trip. `http.RoundTripper`
  already has the right shape, so the port is the caller-injected transport.
  This one seam covers both testing and authentication.
- **Adapter:** `net/http` with a small wrapper that executes a planned request
  and hands the response back to the core.

The public surface is one package, `blob`. Internal helpers move to `internal/`
packages when a file nears the 1,000-line cap (R2), not before.

## Public API sketch

Shapes below are a starting point, not a contract. Expect them to change as
prototypes land.

```go
// Repository addresses a blob store: a registry host plus a repository name.
type Repository struct {
    Host string // "registry.example.com" or "localhost:5000"
    Name string // "library/ubuntu"
}

func New(opts ...Option) *Client

// Client options: WithTransport(http.RoundTripper), WithStorageTransport(http.RoundTripper),
//                 WithRetryPolicy(...), WithPlainHTTP(bool), WithChunkedUpload(chunkSize int64),
//                 WithParallelPull(workers int, chunkSize int64)
// Per-call options: WithProgress(fn func(done, total int64))

func (c *Client) Exists(ctx context.Context, repo Repository, dgst digest.Digest) (bool, error)
func (c *Client) Pull(ctx context.Context, repo Repository, dgst digest.Digest, opts ...TransferOption) (io.ReadCloser, error)
func (c *Client) PullRange(ctx context.Context, repo Repository, dgst digest.Digest, offset, length int64, opts ...TransferOption) (io.ReadCloser, error)
func (c *Client) Push(ctx context.Context, repo Repository, dgst digest.Digest, size int64, r io.Reader, opts ...TransferOption) error
func (c *Client) Mount(ctx context.Context, dst, src Repository, dgst digest.Digest) (bool, error)
```

API decisions:

- `Pull` returns an `io.ReadCloser` instead of writing to an `io.Writer`. A
  reader composes with more caller code and never buffers the blob (P2). The
  reader verifies the digest as bytes flow; the final `Read` returns
  `ErrDigestMismatch` instead of `io.EOF` when the hash does not match.
- `Push` requires the digest and size up front. Registries need the digest to
  commit an upload, and the size sets `Content-Length`. Size is mandatory:
  there is no unknown-length upload, and the client rejects both short and
  trailing input. The reader must reach EOF immediately after that size; a
  streaming producer closes its pipe after the final byte. A caller that does
  not know the size spools the data first and comes back with a number.
- `PullRange` serves partial blobs through a ranged `GET`. It never verifies
  the digest: the digest covers the whole blob, so a partial body cannot be
  checked against it. The client therefore validates every `Content-Range`
  before exposing bytes and follows shorter valid portions until it has the
  requested window. `Pull` is the verified path; callers that need integrity
  on partial reads build it above the library.
- Byte-moving calls take `WithProgress(fn)`. The callback receives cumulative
  bytes moved and the total (`-1` when unknown), runs synchronously on the
  transfer path, and must return quickly. Calls do not overlap within one
  transfer, including a parallel Pull. Concurrent transfers may call the same
  function at the same time, so callers must protect callback state shared
  across transfers. Pull reports bytes delivered to the caller. Monolithic Push
  reports after the final `201`; chunked Push advances after each PATCH
  acknowledgement, so only a nil Push error proves the final commit succeeded.
  A caller-side wrapper cannot report these consistently across transparent
  retries and parallel workers.
- `Mount` returns `(false, nil)` when the registry declines the mount. The
  caller then decides whether to push. Mount-with-automatic-push-fallback can
  be layered on later if real use shows the need.
- Defaults are the code paths every registry serves correctly: monolithic
  upload and single-stream download. Chunked upload and parallel pull exist
  behind toggles and are never chosen automatically.

## Wire behavior

The blob subset of the distribution spec is five request shapes:

| Operation | Request |
|---|---|
| Existence | `HEAD /v2/<name>/blobs/<digest>` |
| Pull | `GET /v2/<name>/blobs/<digest>` |
| Start upload / mount | `POST /v2/<name>/blobs/uploads/` |
| Chunked upload | `PATCH <location>` with `Content-Range` |
| Commit upload | `PUT <location>?digest=<digest>` |

Rules the client follows:

- Validate endpoint-specific success statuses. `200 OK` proves existence or a
  full pull, `206 Partial Content` carries a validated range, `202 Accepted`
  opens or advances an upload session, and `201 Created` completes a commit or
  mount. An unexpected 2xx response is not promoted to a terminal success.
- Resolve `Location` headers as relative or absolute URLs. Preserve the raw
  query bytes because registries use opaque and sometimes signed session
  parameters; append only the digest parameter required for commit.
- Scope the caller's potentially authenticated registry transport to the
  registry origin. Absolute upload locations and cross-origin redirects use a
  separate storage transport with `Authorization`, `Proxy-Authorization`,
  cookies, and `Referer` removed. This routing happens before either transport
  runs, so an auth wrapper cannot add registry credentials to a storage
  request or expose opaque session state through a redirect referrer.
- Follow read redirects normally. A write redirect must preserve the method
  and have a replayable body (`307` or `308`); reject redirects that would turn
  a `POST`, `PUT`, or `PATCH` into a bodyless `GET` instead of accepting the
  redirected `200` as upload success. Redirect targets must remain HTTP(S),
  redirect loops stop at ten hops, and deterministic redirect-policy failures
  are not retried as fresh upload sessions.
- Parse the OCI error body (`{"errors": [...]}`) when present. When the body
  is not that shape, fall back to the status code. A malformed error body is
  never itself an error.
- Upload monolithically (single `PUT`) unless the caller sets
  `WithChunkedUpload`. Chunked upload is spec-optional and broken on major
  hosted registries (ECR discards chunks after the first and still returns
  success; Docker Hub and GHCR have similar reports), because mainstream
  clients never exercise it. It is an explicit opt-in, not a fallback.
- In chunked mode, honor `OCI-Chunk-Min-Length` and verify the `Range` header
  after every `PATCH`. If the acknowledged range does not advance by the
  chunk just sent, abandon the session and fail the upload with a
  descriptive error. The digest-verified commit `PUT` is the backstop: a
  registry that dropped bytes fails the commit rather than storing a bad
  blob. A non-SHA-256 upload names its digest algorithm when opening the
  session.

## Reliability

Retry policy:

- Retry on connection errors, request timeouts, `429`, and `5xx`. Do not
  retry other `4xx`; the request is wrong, not unlucky.
- Exponential backoff with full jitter. Honor `Retry-After` when the registry
  sends it, capped by the policy's maximum delay.
- `MaxAttempts` bounds the complete request, including parallel chunk body
  retries; nested retry loops must not multiply it.
- The caller's `context` stops network work, retries, and backoff immediately
  and remains inspectable through `errors.Is` on the returned error. An
  arbitrary `io.Reader` has no cancellation operation, so `Push` still waits
  for an in-flight source `Read` to return before it gives the reader back to
  the caller. A blocking producer must arrange to unblock its reader when the
  context ends.
- Once an upload session will not be continued, send a best-effort `DELETE`
  to its current location. Cleanup failure never replaces the original
  transfer error.

Downloads resume; uploads restart:

- Download: on a broken stream, issue a ranged `GET` from the last verified
  byte, gated on the registry serving ranges (`Accept-Ranges` or a `206`
  response). The digest state carries across the resume, so no bytes are
  hashed twice.
- Upload: a failed upload restarts from byte zero. The spec defines session
  resume (`GET` on the upload URL returns the received `Range`), but the
  registries that break chunked upload break resume with it, and no
  mainstream client exercises the path. It stays out until a consumer
  demonstrates the need. Restarting requires the caller-supplied reader to
  be re-readable or seekable; when it is not, the upload fails after the
  first attempt.

## Errors

Sentinel errors (E1), kept few and high-level:

- `ErrNotFound`: the blob or repository does not exist.
- `ErrDigestMismatch`: bytes did not hash to the expected digest.

Everything else wraps the underlying HTTP or network error with enough context
to debug. New sentinels are added when a caller demonstrates a need to branch
on the condition, not before.

## Testing

Three layers (T1):

1. Unit tests on the pure core: response interpretation, retry decisions,
   range math, digest bookkeeping.
2. Integration tests with a mockery-generated transport mock, driving the
   client against scripted registry conversations, including the misbehaving
   ones.
3. End-to-end tests with testcontainers against real registries. Start with
   `registry:2` and `zot`; both are OCI-conformant and run cheaply in CI.

The tolerance rules in "Wire behavior" are the heart of the library, so the
scripted-conversation suite is the largest of the three.

## Performance

- Stream everything. No code path holds a whole blob in memory (P2).
- Upload request bodies stage up to four 256 KiB batches. This removes a
  scheduler handoff for every small transport read while preserving prompt
  `Close` and caller-reader ownership. Small bodies allocate proportionally;
  active staging is capped at 1 MiB per request, and the process retains at
  most 2 MiB of idle upload buffers.
- Parallel chunk buffers use a bounded client cache rather than `sync.Pool`.
  The cache retains at most one buffer per configured worker and drops excess
  buffers returned by concurrent pulls.
- Connection reuse and HTTP/2 come from the transport. For parallel pulls, the
  client sizes its library-owned default HTTP/1 idle pool to the worker count;
  caller-supplied transports remain caller-tuned. The client never defeats
  reuse by closing bodies early or rebuilding clients.

Parallel pull is the library's one extra feature. It is off by default;
`WithParallelPull(workers, chunkSize)` turns it on.

- `Pull` keeps the same signature and still returns a verifying
  `io.ReadCloser`. Workers fetch ranged `GET`s concurrently; the reader
  emits chunks in order, so digest verification works unchanged.
- A fixed worker set handles every chunk. Goroutine and task-channel counts
  therefore follow the configured worker count rather than the blob's chunk
  count.
- Payload buffering is bounded by roughly `workers × chunkSize`, and active
  response bodies are bounded by `workers`; the initial range probe consumes
  one of those worker slots. This is the one deliberate exception to "never
  buffer", and the caller sets that bound.
- Validate each ranged response's start, end, and total. A registry may return
  a shorter valid portion, which the same worker completes without changing
  output order. One scheduled chunk accepts at most 16 successful portions, so
  a pathological server cannot turn one range into an unbounded request loop.
- Closing the reader cancels the probe, queued work, active bodies, and retry
  backoff before returning. A progress callback may request that close without
  waiting on its own worker result.
- If the registry does not serve ranges, `Pull` falls back to a single
  stream. The toggle states intent, not a requirement.
- A scatter-write variant (`io.WriterAt` sink) was rejected: it would add a
  second pull API and break streaming verification for little gain.

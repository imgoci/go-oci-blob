# Implementation Plan

Breaks the merged design (`docs/docs/explanation/design.md`, master) into
phases. Executing every phase implements the full design. Each phase is one
PR, ends with passing functional tests, and leaves master releasable. Order
follows the dependency chain: read paths before write paths, correctness
before reliability, reliability before the optimized toggles.

All production code lives in the root package `blob`
(`github.com/imgoci/go-oci-blob`). The port is `http.RoundTripper`; mockery
generates its mock into `mocks/` per T2/T3. E2E tests use testcontainers
against `registry:2` and `zot` and skip under `-short`.

## Phase 1 — Skeleton and Exists

Deliverable: a client that can ask a real registry whether a blob exists.

- `client.go`: `Client`, `New`, client options (`WithTransport`,
  `WithPlainHTTP`; retry/toggle options stubbed in later phases).
- `repo.go`: `Repository` type and validation.
- `wire.go`: request building (URL construction, `Location` resolution) and
  response interpretation (status families, OCI error body with fallback).
  This is pure core logic; it must test without a network.
- `errors.go`: `ErrNotFound`.
- `exists.go`: `Exists` via `HEAD`.
- Test scaffolding for the whole project: testify + mockery dev deps,
  `.mockery.yml`, `mocks/` for the transport, `e2e_test.go` harness with
  testcontainers (`registry:2`, `zot`).

Exit: unit tests on `wire.go`; `Exists` passes e2e against both registries.

## Phase 2 — Pull and PullRange

Deliverable: verified downloads.

- `pull.go`: `Pull` returning the verifying `io.ReadCloser`; `PullRange`
  (unverified by design); redirect handling.
- `verify.go`: digest-verifying reader (`ErrDigestMismatch` instead of
  `io.EOF` on mismatch) added to `errors.go`.
- `TransferOption` type lands here (empty for now) so signatures are final.

Exit: unit tests for the verifying reader including the mismatch path;
scripted-transport integration tests for redirects and garbage error bodies;
e2e pull round-trips on both registries.

## Phase 3 — Push and Mount

Deliverable: monolithic upload and cross-repo mount.

- `push.go`: `POST` then `PUT` monolithic upload; size mandatory.
- `mount.go`: `Mount` returning `(false, nil)` on decline.

Exit: integration tests for status-family tolerance and relative/absolute
`Location`; e2e: push then pull the same bytes back on both registries;
mount against a registry that supports it and one scripted decline.

## Phase 4 — Reliability

Deliverable: retries and resume as designed; this phase is the heart of the
library and gets the largest scripted-conversation suite.

- `retry.go`: policy type, `WithRetryPolicy`, full-jitter backoff,
  `Retry-After`, retry classification (connection errors, timeouts, 429,
  5xx), context cancellation.
- `pull.go`: ranged-`GET` resume from the last verified byte, gated on
  range support; digest state carries across resume.
- `push.go`: restart-from-zero on failure when the reader is re-readable;
  clean error when it is not.

Exit: integration tests scripting broken streams, 429/503 with and without
`Retry-After`, mid-body disconnects, and non-resumable readers; e2e happy
paths still green.

## Phase 5 — Chunked upload toggle

Deliverable: `WithChunkedUpload(chunkSize)` exactly as the design's wire
rules state.

- `chunked.go`: `PATCH` loop, `OCI-Chunk-Min-Length`, `Range`-ack
  verification after every `PATCH`, session abandonment with a descriptive
  error when the ack does not advance.

Exit: integration tests including a scripted ECR-style registry that
acks without advancing; e2e chunked push against both registries.

## Phase 6 — Parallel pull toggle

Deliverable: `WithParallelPull(workers, chunkSize)`.

- `parallel.go`: concurrent ranged workers, in-order emission through the
  existing verifying reader, memory bound of `workers × chunkSize`,
  single-stream fallback when ranges are unsupported.
- Benchmarks comparing single-stream and parallel pull; buffer reuse via
  `sync.Pool` lands here with the copy loops it serves.

Exit: race-detector-clean tests for ordering and fallback; benchmark
results recorded in the PR; e2e parallel pull on both registries.

## Phase 7 — Progress

Deliverable: `WithProgress(fn)` on `Pull`, `PullRange`, and `Push`.

- `progress.go`: the `TransferOption`, committed-progress accounting (no
  double-count across retries/restarts), aggregated reporting for parallel
  pull.

Exit: integration tests assert monotonic counts across a scripted retry;
unit tests for aggregation.

## Phase 8 — Docs and v0.1.0

Deliverable: user documentation and the first release.

- `docs/docs/`: tutorial (push/pull with an authenticated transport from
  ORAS), how-to pages (retry tuning, chunked upload, parallel pull,
  progress), reference page for the public API, design page updated with
  any drift from implementation (D5/D6).
- Godoc examples for the public API boundary (D2).
- Merge the Release Please PR for v0.1.0 (release app must be installed).

Exit: docs build in CI; released tag installable via `go get`.

## Standing rules for every phase

- Update the design doc in the same PR whenever implementation forces a
  decision the doc does not record (D6); the doc stays the authority.
- No new runtime dependencies beyond stdlib + go-digest; test-only deps are
  fine.
- Phases are scoped, not sacred: if a phase's prototype shows the design is
  wrong somewhere, stop and revise the design doc first (agile note in
  TECH_NOTES.md).

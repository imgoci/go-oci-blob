---
id: 002
title: Implement phases 1-7 of the blob transfer library
date: 2026-08-10
status: complete
repos_touched: [go-oci-blob]
related_sessions: [001]
---

## Goal

Execute the session 001 implementation plan (`.journal/001/PLAN.md`),
starting at Phase 1. Scope grew by user direction to phases 1 through 7 in
one sitting, one reviewed-and-squash-merged PR per phase.

## Outcome

Goal met. The library is functionally complete per the design doc: Exists,
Pull (verified, resuming, optionally parallel), PullRange, Push (monolithic
and verified-chunked, restarting), Mount, retries, and progress reporting
are all merged to master with three-layer test coverage (units, scripted
mock-transport conversations, testcontainers e2e against registry:2 and zot
v2.1.20, all race-detector clean). Runtime deps remain stdlib + go-digest.
Only Phase 8 (docs + v0.1.0) remains.

## Key Decisions

- Test harness defaults to a zero RetryPolicy (`newTestContext`) -> default-on
  retries otherwise re-consume scripted mock responses and drain shared
  bodies; retry tests opt into a fast millisecond policy.
- E2E tests live behind a `//go:build e2e` tag (user request) -> plain
  `go test ./...` stays Docker-free; a `test-e2e` moon task in `root:check`
  keeps CI coverage; `.golangci.yml` gained `run.build-tags: [e2e]` so the
  tagged file stays linted.
- Parallel pull probes with the first chunk's ranged GET -> a 206 carries the
  total via Content-Range, a 200 already IS the single-stream fallback, so
  range detection costs zero extra requests.
- Chunked upload ack-not-advancing is a non-retryable abandonment -> ECR-style
  registries drop chunks deterministically; retrying wastes time and the
  descriptive error names the known failure mode.
- Push restarts rewind to the reader's captured starting position, not byte
  zero -> a reader handed over mid-file (e.g. positioned os.File) must not
  replay bytes before its start.
- Monolithic push progress uses high-water-mark suppression; chunked push
  reports only verified acks -> the design's "committed progress, no
  double-count across restarts" promise.
- Blob seeding in e2e via raw HTTP POST+PUT -> read-path tests stay
  independent of Push bugs.

## Changes

All in go-oci-blob, one squash commit per phase on master:

- PR #10 (93dc58c) Phase 1 — client/repo/wire/errors/exists skeleton, testify
  + mockery scaffolding (mockery 3.7.2 pinned via mise aqua backend),
  testcontainers e2e harness; plus the e2e build-tag follow-up commit.
- PR #12 (7f58c57) Phase 2 — verifying Pull, unverified PullRange with
  ignored-range windowing, TransferOption placeholder, ErrDigestMismatch.
- PR #13 (6af7ba4) Phase 3 — monolithic Push (POST+PUT, mandatory size),
  Mount with (false, nil) decline.
- PR #14 (e2e62fa) Phase 4 — RetryPolicy/WithRetryPolicy (default on,
  4×/250ms/30s), full-jitter backoff, Retry-After, doRetry; pull resume from
  last delivered byte under the verifying reader; push whole-flow restart.
- PR #15 (395712c) Phase 5 — WithChunkedUpload: verified PATCH loop,
  OCI-Chunk-Min-Length, Range-ack verification with abandonment.
- PR #16 (ac990d4) Phase 6 — WithParallelPull: ordered-futures pipeline,
  memory ≈ workers×chunk, sync.Pool buffers, probe/fallbacks, benchmarks
  (loopback parity; wins expected on high-latency links, recorded in PR).
- PR #17 (d6c256d) Phase 7 — WithProgress: nil-safe monotonic tracker wired
  through all five transfer paths.

## Open Threads

- Phase 8 remains: Diátaxis docs, godoc examples, design-doc sync, and the
  first release.
- Release Please PR #11 proposes v1.0.0, but the plan says v0.1.0 — configure
  `initial-version`/`bump-minor-pre-major` (or `release-as`) before merging.
  The `imgoci-release-please` app installation on the repo is still
  unverified (carried from session 001).
- Dependabot PRs #18/#19 (actions/checkout 7.0.1, actions/cache 6.1.0)
  arrived 2026-08-11 and are untouched.
- `moon run` is broken locally on machines with a legacy `~/.proto` store
  (see TECH_NOTES); fixing it is a template-go-level decision.
- The design doc needed no updates this session (no drift), but Phase 8's
  docs pass should confirm that holds.

## References

- PRs: #10, #12, #13, #14, #15, #16, #17 (all squash-merged);
  #11 (release-please, open), #18/#19 (dependabot, open).
- Plan: `.journal/001/PLAN.md`; design: `docs/docs/explanation/design.md`.
- Prior session: `.journal/001/SUMMARY.md`.

## Lessons

- moon v2 prepends `~/.proto/{tools/proto,shims,bin}` to every task PATH, so
  a stale proto store shadows mise pins locally while CI stays clean. Verify
  task commands with `mise exec --` when moon misbehaves.
- golangci-lint's shared cache replays findings for deleted worktree paths:
  the generated-file filter fails open when it cannot re-read the file, so
  previously-suppressed mockery-mock findings leak. `golangci-lint cache
  clean` after removing a worktree.
- An unexpected mockery call inside a worker goroutine runs t.FailNow →
  runtime.Goexit, so the goroutine's result future never fills and cleanup
  paths deadlock. Script prospective concurrent requests with `.Maybe()` in
  failure-path tests.
- Retry-After's date form needs `http.TimeFormat` (GMT); `time.RFC1123` with
  a UTC zone silently fails http.ParseTime.

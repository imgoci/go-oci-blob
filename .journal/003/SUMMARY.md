---
id: 003
title: Correctness and protocol hardening
date: 2026-08-11
status: complete
repos_touched: [go-oci-blob]
related_sessions: [001, 002]
---

## Goal

Fix the panic paths, consumer surprises, reproducible transfer bugs,
HTTP/OCI protocol mistakes, and dropped or misleading errors found by a
repository-wide correctness audit.

## Outcome

Goal met. PR #20 was approved, passed CI and CodeQL, and was squash-merged to
`master` as `c9b5bd159eca2d972a5add281fc96b2876ba5d1f`. The confirmed pull,
resume, range, parallelism, upload, redirect, retry, cleanup, nil-input,
overflow, progress, and error-propagation defects are fixed. Focused
adversarial reviews found no remaining reproducible issue in the audited
slices.

## Key Decisions

- Validate endpoint-specific HTTP and OCI statuses instead of accepting every
  2xx response -> a generic success class can report incomplete uploads as
  committed and accept malformed range conversations.
- Preserve upload-session query bytes while replacing only `digest`, and
  resolve relative Locations against the final request URL -> registries may
  use opaque, order-sensitive state and redirect an operation before issuing a
  new Location.
- Route only same-origin, explicitly marked registry requests through the
  caller's authenticated transport -> absolute storage Locations are new
  requests, not redirects, so standard redirect credential stripping alone is
  insufficient.
- Treat `MaxAttempts` as one lifetime budget across probes, retries, resumes,
  fallbacks, and parallel chunks -> nested retry loops must not multiply the
  caller's configured limit.
- Stream uploads through an ownership-aware pipe and exact-size reader -> this
  honors asynchronous `RoundTripper` request-body behavior, keeps cancellation
  responsive, rejects short or trailing input, and avoids whole-blob buffering.
- Bound parallel worker allocation and validate arithmetic before building
  ranges or buffers -> caller-controlled options must return safe behavior
  instead of overflowing or panicking in an internal goroutine.

## Changes

- `client.go`, `transport.go`, and `repo.go` - added origin-scoped registry and
  storage transports, safe redirect policy, canonical target validation, and
  nil-safe options.
- `pull.go`, `resume.go`, `parallel.go`, and `wire.go` - added strict range and
  status validation, bounded response bodies, lifetime retry accounting,
  cancellation-safe close behavior, and parallel allocation guards.
- `push.go`, `chunked.go`, `mount.go`, and `retry.go` - fixed exact-size body
  handling, zero-byte requests, replay ownership, Location/query semantics,
  upload cleanup, terminal statuses, cancellation identity, and delay overflow.
- `README.md` and `docs/docs/explanation/design.md` - documented the corrected
  consumer-visible behavior and transport boundaries.
- Correctness, reliability, integration, and protocol tests - added focused
  regressions for every confirmed defect and the follow-up edge cases found
  while implementing the fixes.

## Open Threads

- No known correctness issue from this audit remains open.
- Phase 8 documentation and the first release remain outside this session, as
  do the existing Release Please and Dependabot work recorded in session 002.
- The local Moon wrapper still encounters the legacy `~/.proto` Go-toolchain
  mismatch documented in `TECH_NOTES.md`; direct mise-driven constituent gates
  and hosted CI pass.

## References

- PR #20: https://github.com/imgoci/go-oci-blob/pull/20
- Authoritative design: `docs/docs/explanation/design.md`
- Prior implementation summary: `.journal/002/SUMMARY.md`
- Original design and plan: `.journal/001/SUMMARY.md` and
  `.journal/001/PLAN.md`

## Lessons

- Go's redirect credential stripping does not protect a fresh request built
  from an absolute registry Location; credential scope must be enforced at the
  transport boundary.
- A conforming `RoundTripper` may continue consuming or closing a request body
  asynchronously after returning, so upload replay and caller-reader ownership
  need an explicit handoff.
- OCI interoperability depends on exact status, Location, query, and range
  semantics; broad HTTP-success helpers hide protocol failures.

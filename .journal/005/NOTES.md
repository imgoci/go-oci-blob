---
id: 005
title: Start a new work session
started: 2026-08-11
---

## 2026-08-11 16:22 — Kickoff

Goal for the session: Start a fresh journal session and await the user's project request.
Current state of the world: Sessions 001 through 004 are closed; the blob transfer implementation, correctness hardening, and verified performance fixes are merged, while the next project task has not yet been stated.
Plan: Bind session 005, receive the user's scoped request, and record meaningful checkpoints as the work progresses.

## 2026-08-11 17:53 — Manual functional campaign planned

Goal: Propose a thorough, non-permanent manual functional test of every real consumer-facing library promise, then pause for review before executing it.
Current state: The exact target is clean `master` at `1bc039c48092b1a29eda6dcf678a06fd87468a4b`. The repository has 95 top-level tests across 23 test files, but its live E2E layer is limited to unauthenticated plain HTTP against Registry 2 and Zot with roughly 1 MiB payloads.
Plan: Export the exact Git tree to a read-only temporary snapshot; compile a disposable external consumer module; run the unchanged repository corpus as calibration; then exercise real registries, a real-socket fault laboratory, shared-client race/concurrency stress, cancellation and cleanup, origin credential boundaries, large-stream resource bounds, and independent digest verification. Keep all harness code and evidence outside the repository, create no project branch or PR, and verify the worktree remains unchanged.
High-risk probes: Measure the literal `RetryPolicy.MaxAttempts` boundary across parallel chunks and PullRange continuations, and test whether one reused progress callback can overlap across concurrent transfers despite the unqualified serialization wording.
Next: Await user review of the proposed campaign before creating any harness, container, VM, or hosted repository.

## 2026-08-12 01:47 — Consumer campaign executed

Target and isolation: Exported commit `1bc039c48092b1a29eda6dcf678a06fd87468a4b` (tree `a530b8b10212449ccef2becb8ea434cf10791cda`) to a read-only temporary source tree and built a separate disposable Go consumer module. The project checkout remained clean and no library files were changed.

Passing evidence: The unchanged build, unit, race, Registry 2/Zot E2E, E2E-race, and benchmark calibration all passed. External consumer tests passed against Registry 2, Zot, a TLS plus Basic-auth Registry 2, GHCR, real-socket truncation/resume, retry/status faults, exact range continuations, integrity failures, redirects and credential stripping, monolithic/chunked upload protocols, mount cleanup, exact reader sizes, cancellation, shared-client mixed-operation stress, large streaming transfers, connection reuse, and resource cleanup. The shared-client race campaign completed 648 mixed operations across `GOMAXPROCS=1,2,8` without a race, deadlock, or data mix-up. Large 64 MiB serial/parallel Pull and non-seekable Push measurements stayed far below payload size and reused bounded connections.

Contract findings: `RetryPolicy.MaxAttempts` says one operation has one total attempt budget, while the parallel-pull design and tests apply the budget per logical range request. A parallel Pull configured for two attempts made seven wire requests across four ranges while every range stayed within two attempts; this is a documentation ambiguity, not a proven reliability failure. `WithProgress` says callbacks never run concurrently with themselves, but reusing one callback across two concurrent transfers caused overlap because serialization is per transfer. The callback probe is retained as intentionally failing literal-contract evidence rather than a green characterization test.

Hosted characterization: GHCR accepted an unreferenced blob with `201` and answered HEAD as present but returned `BLOB_UNKNOWN` to GET; an ORAS blob-only control behaved the same. After ORAS linked the same bytes through a minimal manifest, independent ORAS fetch plus library serial and parallel Pull all verified the exact bytes. Every uniquely named GHCR package was deleted and confirmed absent.

Next: Finish the aggregate normal/race reruns after final harness review, verify all temporary processes/containers/hosted packages are gone, record artifact hashes and source immutability, then report the pass/fail matrix without merging any test code.

## 2026-08-12 02:02 — Final evidence and cleanup audit

Aggregate result: The final positive consumer suite passed 32 top-level tests and 118 test nodes with zero skips or failures. A focused race run passed 23 protocol/fault/TLS top-level tests and 59 nodes. The resource lane passed three normal repetitions plus race; the concurrency lane passed 648 mixed operations across `GOMAXPROCS=1,2,8` plus a revised three-repeat race check. The strict cross-transfer callback probe failed reproducibly in all three repetitions, confirming that the same callback can overlap when reused by concurrent transfers even though ordinary per-transfer callback serialization passed.

Final classifications: No functional data-integrity, retry, streaming, shared-client race, cleanup, TLS/auth, Registry 2, Zot, or GHCR lifecycle defect was found. The remaining actionable contract issue is the unqualified `WithProgress` serialization wording. The parallel retry wording is ambiguous between operation and logical-request scopes. The design's advertised-range gate for broken-stream resume is stale in a reliability-positive direction: the implementation resumes without the advertisement and safely accepts a full-200 fallback. The README's claim that no public API exists is also stale.

Cleanup and proof: A fresh `git archive` diff against the temporary source was empty; the source has zero user-writable files; the real checkout is still clean at the same commit and tree. All current-run Registry 2, Zot, TLS registry, and Ryuk containers were removed. The only Docker resources left are one unrelated running GitHub MCP container and two unrelated stale Aug 8 resources that predated the campaign. No uniquely named GHCR functional package remains. Harness/evidence checksums are in `/tmp/go-oci-blob-functional.quDtZu/evidence/SHA256SUMS`; the resource report is `resource-lane.md`.

Next: Deliver the final manual functional-test verdict. No test harness or library change will be merged.

## 2026-08-12 10:04 — Progress callback contract fix published

Decision: Fix the functional campaign's one actionable contract finding by clarifying the API rather than imposing global callback serialization. `WithProgress` now states that callbacks do not overlap within one transfer, while concurrent transfers may invoke a reused callback at the same time and therefore require caller-side synchronization of shared state. The design explanation carries the same boundary.

Test change: Made the public progress recorder race-safe and added a common assertion that callback invocations observed within each tested transfer do not overlap. Kept the test at the public API boundary; an implementation-coupled tracker concurrency test had intentionally been removed in PR #23. Did not add a test requiring cross-transfer overlap because the clarified contract permits overlap without promising it.

Verification: Focused progress tests passed 20 normal repetitions and five race repetitions. The full build, unit suite, race suite, fresh-cache golangci-lint format/lint checks, strict MkDocs build, and tagged Registry 2/Zot E2E suite all passed. The initial lint invocation read stale cache paths from a deleted sibling worktree; rerunning with an isolated fresh cache passed cleanly.

Published: Commit `2d238b6` (`fix: clarify progress callback concurrency contract`) on `feat/progress-callback-contract`; PR #24: https://github.com/imgoci/go-oci-blob/pull/24.

Next: Await review and hosted checks. Do not merge or close session 005 without explicit user direction.

## 2026-08-12 10:05 — Hosted checks green

PR #24 passed CI, both Go and Actions CodeQL analyses, and the GitHub Pages validation. The PR remains open and unmerged for user review.

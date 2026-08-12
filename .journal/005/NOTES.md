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

Strict contract findings: Two public statements are falsified by deterministic probes. `RetryPolicy.MaxAttempts` says one operation has one total attempt budget, but a parallel Pull configured for two attempts made seven requests because each later chunk received a fresh budget. `WithProgress` says callbacks never run concurrently with themselves, but reusing one callback across two concurrent transfers caused overlap because serialization is per transfer. Both probes are retained as intentionally failing strict-contract evidence rather than green characterization tests.

Hosted characterization: GHCR accepted an unreferenced blob with `201` and answered HEAD as present but returned `BLOB_UNKNOWN` to GET; an ORAS blob-only control behaved the same. After ORAS linked the same bytes through a minimal manifest, independent ORAS fetch plus library serial and parallel Pull all verified the exact bytes. Every uniquely named GHCR package was deleted and confirmed absent.

Next: Finish the aggregate normal/race reruns after final harness review, verify all temporary processes/containers/hosted packages are gone, record artifact hashes and source immutability, then report the pass/fail matrix without merging any test code.

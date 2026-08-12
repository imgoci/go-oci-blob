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

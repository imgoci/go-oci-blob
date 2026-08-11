---
id: 003
title: Correctness and protocol hardening
started: 2026-08-11
---

## 2026-08-11 08:46 — Kickoff
Goal for the session: Address all correctness issues identified by the repository-wide audit, including panic paths, consumer-facing surprises, reproducible transfer bugs, HTTP/OCI protocol mistakes, and dropped or misleading errors.
Current state of the world: Master is clean at d6c256d52f03eafe3cda31e2753e9c5128a8b335. The normal race, E2E, vet, and lint gates pass, while disposable focused repros confirmed defects across range handling, terminal status validation, pull shutdown, upload redirects and locations, size enforcement, retries, progress, session cleanup, cancellation, and numeric boundaries.
Plan: Create an isolated Worktrunk feature branch from the fetched default branch, add focused regression tests in small batches, implement the corresponding fixes, update user-visible documentation and design decisions, and run the complete validation matrix.

## 2026-08-11 10:12 — Correctness hardening implemented
Feature branch `feat/correctness-hardening` now contains commit `3a683d7f5b9eab2d35611f7334f8a3a6d121da8f` (`fix: harden OCI transfer correctness`). The change fixes the audited pull, range, resume, parallelism, upload, retry, cleanup, status, redirect, query-preservation, nil-input, overflow, progress, and error-propagation defects. Registry credentials are now scoped to the registry origin, off-origin storage requests strip sensitive headers, and write redirects enforce HTTP(S), replay, and loop policy. Upload bodies enforce exact size without whole-blob buffering, honor asynchronous `RoundTripper` ownership, unblock transport reads on close, and never access the caller's reader after `Push` returns. Pull requests validate every range, keep one lifetime retry budget, and bound configured concurrency safely.

Validation passed on the final tree: `go test -race -shuffle=on -count=10 ./...`, focused adversarial race suites up to 100 repetitions, `go vet ./...`, `golangci-lint run`, `golangci-lint fmt --diff`, `go build ./...`, coverage at 87.1%, strict MkDocs build, and race-enabled E2E tests against the configured `registry:2` and Zot matrix. Three independent final review passes found no remaining reproducible defect in their assigned slices. The aggregate Moon check remains unusable in this worktree because its system-toolchain environment pairs a Go 1.26.5 binary with Go 1.26.4 tools; every constituent gate was run directly through mise and passed. The feature commit is local only; no feature push or pull request was requested.

## 2026-08-11 10:16 — Pull request opened
Pushed `feat/correctness-hardening` at `3a683d7f5b9eab2d35611f7334f8a3a6d121da8f` and opened ready-for-review PR #20: https://github.com/imgoci/go-oci-blob/pull/20. It targets `master` with the conventional title `fix: harden OCI transfer correctness`. GitHub reports the PR as open and mergeable; hosted checks had not started when the PR was verified. No merge or session close was requested.

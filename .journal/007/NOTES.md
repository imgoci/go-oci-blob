---
id: 007
title: Remediate upstream requirements
started: 2026-08-13
---

## 2026-08-13 10:52 — Kickoff
Goal for the session: Read `GO_OCI_BLOB_UPSTREAM_REQUIREMENTS.md`, develop a remediation plan with a planning agent, review and refine it, then propose it for human review.
Current state of the world: The repository has released version 1.0.0 after completing implementation, hardening, performance, compatibility, and user-documentation work; the upstream requirements have not yet been assessed in this session.
Plan: Read the requirements and relevant repository context, delegate a first remediation plan, validate and revise that plan against the codebase, and present the resulting plan without implementing it.

## 2026-08-13 11:16 — Remediation plan ready for review
Read the upstream handoff and inspected the retry, response-error, origin-routing, redirect, upload-body, progress, documentation, verification, and release mechanisms. A focused planning agent produced the first plan; the review retained its file map and security findings but corrected three design risks: caller cancellation needs an authoritative terminal classification rather than an `errors.Is(context.DeadlineExceeded)` shortcut; rejected redirect replay bodies must be closed only on request-error paths, not after successful responses; and wire callbacks must run outside `uploadBody.stateMu`.

Proposed implementation order: add origin-aware safe response errors, registry-only sentinels, `StatusCode`, and typed retry metadata; harden upload locations and all ordinary error rendering; add strict write redirects while preserving the default; add transport-consumption wire progress across monolithic, chunked, retry, and redirect bodies; update Godoc and user documentation; add root race coverage to the aggregate check; then release through Release Please as v1.1.0. No implementation files were changed. Next step is human review of the proposed plan.

## 2026-08-13 12:07 — Upstream remediation released
Implemented and merged the reviewed remediation in four focused pull requests: PR #30 added origin-aware safe response errors, registry-only sentinels, `StatusCode`, and durable `Retryable` metadata; PR #32 hardened upload locations and rendered errors and added strict write-redirect control; PR #33 added transport-consumption wire progress with serialized lifecycle-safe callbacks; PR #34 added package and embedding documentation and made `root:test-race` part of the aggregate check.

`moon run root:check` passed with format, lint, build, unit, race, E2E, compatibility, and strict documentation tasks. The embedding reference rendered correctly in the local MkDocs preview. Release Please generated PR #31, which was reviewed and merged; the resulting draft was published as `v1.1.0`. The public release and tag both resolve to `220500bf6cb0a81f5c968662ca32181c9106ae79`, and `go list -m github.com/imgoci/go-oci-blob@v1.1.0` resolves the released module. The temporary upstream requirements handoff was deleted after completion. Bigoci can now pin `v1.1.0` and integrate without parsing HTTP response text or duplicating blob protocol logic.

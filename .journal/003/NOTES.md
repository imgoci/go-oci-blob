---
id: 003
title: Correctness and protocol hardening
started: 2026-08-11
---

## 2026-08-11 08:46 — Kickoff
Goal for the session: Address all correctness issues identified by the repository-wide audit, including panic paths, consumer-facing surprises, reproducible transfer bugs, HTTP/OCI protocol mistakes, and dropped or misleading errors.
Current state of the world: Master is clean at d6c256d52f03eafe3cda31e2753e9c5128a8b335. The normal race, E2E, vet, and lint gates pass, while disposable focused repros confirmed defects across range handling, terminal status validation, pull shutdown, upload redirects and locations, size enforcement, retries, progress, session cleanup, cancellation, and numeric boundaries.
Plan: Create an isolated Worktrunk feature branch from the fetched default branch, add focused regression tests in small batches, implement the corresponding fixes, update user-visible documentation and design decisions, and run the complete validation matrix.

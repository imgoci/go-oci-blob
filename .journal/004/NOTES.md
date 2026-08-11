---
id: 004
title: Apply verified transfer performance fixes
started: 2026-08-11
---

## 2026-08-11 14:04 — Kickoff
Goal for the session: Apply every actionable performance fix from the PR #20 review, verify each fix empirically, and open a pull request.
Current state of the world: PR #20 is merged on master and fixed the correctness audit, but profiling found upload pipe synchronization overhead, parallel-pull allocation and copy overhead, deterministic redirect retry amplification, undersized default HTTP/1 idle connection reuse, and several lower-priority transfer-path costs.
Plan: Create an isolated implementation worktree, add focused behavioral and benchmark coverage, optimize one hot path at a time, run race and end-to-end verification, and publish a conventional-commit pull request.

## 2026-08-11 14:10 — Branch and first verified fix
Created `feat/transfer-performance` from current `master` in its own Worktrunk worktree. The first commit, `3d74d0a`, clones and sizes only library-owned default HTTP transports for the configured parallel worker count. The focused HTTP/1 regression changed repeated four-worker pulls from accumulating connections to reusing at most the initial worker set; constructor tests also prove the process default and caller-supplied transports remain untouched. Upload and parallel-path prototypes are being developed and benchmarked independently before integration.

## 2026-08-11 14:29 — Pull-path fixes committed
Correction to the prior checkpoint: the transport commit was amended during its final safety pass; its final SHA is `4ef2e02`, superseding `3d74d0a`. Commit `4e80691` now replaces per-chunk goroutines and future channels with a fixed worker set, reads ranged responses directly into bounded chunk buffers, caps the client cache at one buffer per configured worker, avoids retrying deterministic redirect failures, and tracks rejected response bodies so `Close` can interrupt them. Focused race tests passed 20 times. Directional measurements showed 57–61% lower allocation bytes for four- and eight-worker pulls without a throughput regression; uncontended integrated benchmarks remain pending before the PR is published.

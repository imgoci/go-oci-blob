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

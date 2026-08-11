---
id: 004
title: Apply verified transfer performance fixes
started: 2026-08-11
---

## 2026-08-11 14:04 — Kickoff
Goal for the session: Apply every actionable performance fix from the PR #20 review, verify each fix empirically, and open a pull request.
Current state of the world: PR #20 is merged on master and fixed the correctness audit, but profiling found upload pipe synchronization overhead, parallel-pull allocation and copy overhead, deterministic redirect retry amplification, undersized default HTTP/1 idle connection reuse, and several lower-priority transfer-path costs.
Plan: Create an isolated implementation worktree, add focused behavioral and benchmark coverage, optimize one hot path at a time, run race and end-to-end verification, and publish a conventional-commit pull request.

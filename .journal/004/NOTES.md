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

## 2026-08-11 15:01 — Integrated fixes and final evidence
Commit `4c20700` replaces the upload body's per-read pipe rendezvous with four bounded 256 KiB batches while preserving streaming producers, cancellation, source ownership, redirects, and exact byte accounting. Follow-up `36388f7` keeps the proportional buffer-count calculation portable on 32-bit systems. Active upload staging is capped at 1 MiB per request and idle retention at 2 MiB process-wide. Commit `430b5b2` also caps a pathological registry at 16 successful range fragments per scheduled chunk, preventing unbounded successful-request amplification.

Balanced ABBA benchmarks on the Apple M4 Max showed monolithic and 1 MiB-chunked loopback Push throughput improving 40% and 37%. The in-memory Push ceiling rose from 11.57 GiB/s to 35.88 GiB/s. Pacing proved the optimized Push tracks 1 and 4 GiB/s network caps and reaches 6.78 GiB/s against an 8 GiB/s cap. Parallel Pull throughput stayed statistically unchanged at four and eight workers while allocation bytes fell 76% and 71%; the 32 KiB high-chunk-count case stayed unchanged in throughput while allocation bytes fell 92%. Eight-worker HTTP loopback improved 16% with 61% lower allocation bytes. Single-stream Pull tracks caps through roughly 2 GiB/s and reaches about 2.6 GiB/s on this host. The remaining parallel copy accounts for only 0.8% of CPU samples, so the riskier `WriterTo` lifecycle was deliberately not added. Mount-to-Push upload-session handoff remains an explicit future API opportunity rather than unsafe implicit caching.

Final local gates passed: race plus shuffled tests ten times, race-enabled end-to-end tests, focused race stress tests, HTTP/1 reuse 100 times, `go vet`, `go build`, coverage (88.6%), strict GolangCI lint and formatting, strict MkDocs build, and Linux/386 cross-compilation.

## 2026-08-11 15:11 — Performance suite isolated
Commit `c0d40a4` moves the Pull and Push benchmarks plus public upload and transport performance regressions from the root into `internal/perf`. The subpackage uses its own generated-mock test context and imports only the public blob API; production code and benchmark names are unchanged. White-box buffer-capacity and connection-constructor tests remain beside the root implementation because relocating them would require exposing internals or using brittle reflection.

The new package passed a one-iteration smoke of every sub-benchmark, 20 focused race repetitions, the complete shuffled race suite ten times, tagged registry end-to-end tests, vet, build, coverage, lint, and formatting. Root statement coverage remains 88.6%. Historical root-package benchmark files need `benchstat -ignore pkg` when compared to results emitted from the new package path.

## 2026-08-11 15:26 — PR merged
PR #21 was squash-merged through GitHub as `0251e8c` (`perf: remove transfer hot-path bottlenecks (#21)`) with an exact `c0d40a4` head guard. GitHub had already merged PR #22 onto `master`; it reported #21 clean against that parent and the squash applied without conflicts. The remote feature branch was deleted automatically. Post-merge CI, GitHub Pages, Release Please, and both CodeQL workflows passed on the exact merge commit.

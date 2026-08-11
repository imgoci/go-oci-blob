---
id: 004
title: Apply verified transfer performance fixes
date: 2026-08-11
status: complete
repos_touched: [go-oci-blob]
related_sessions: [002, 003]
---

## Goal

Remove every actionable transfer hot-path bottleneck remaining after the PR #20 correctness work, prove each change with benchmarks and profiles, and establish where the library becomes CPU-limited rather than network-limited.

## Outcome

Goal met. PR #21 was reviewed and squash-merged to `master` as `0251e8c`. It removes the measured upload synchronization bottleneck, bounds parallel-pull allocation and request amplification, improves default HTTP/1 connection reuse, and moves the performance suite into `internal/perf`. All local verification and post-merge CI, CodeQL, GitHub Pages, and Release Please workflows passed.

On the Apple M4 Max test host, monolithic and 1 MiB-chunked HTTP uploads improved 40% and 37%; the in-memory upload ceiling rose 210%. Eight-worker HTTP pull improved 16% with 61% fewer allocation bytes, while in-memory pull throughput remained stable and allocation bytes fell 71-92%. Controlled pacing showed single-stream Pull tracking the network through roughly 2 GiB/s before a roughly 2.6 GiB/s CPU ceiling, and Push tracking through 4 GiB/s before reaching 6.78 GiB/s against an 8 GiB/s cap.

## Key Decisions

- Replace the per-read upload pipe rendezvous with four bounded 256 KiB staging buffers -> this restores throughput while preserving prompt cancellation, caller-reader ownership, redirect replay, exact byte accounting, and demand-driven streaming.
- Use a fixed parallel-pull worker set and a bounded per-client chunk cache -> this removes per-chunk goroutines and futures while making retained memory deterministic at no more than one chunk buffer per configured worker.
- Tune only library-owned default HTTP transports -> parallel clients retain enough idle HTTP/1 connections without mutating `http.DefaultTransport` or caller-supplied transports.
- Cap each scheduled chunk at 16 successful partial responses and stop retrying deterministic redirect failures -> compliant-but-pathological registries can no longer amplify one chunk into unbounded requests.
- Do not add a `WriterTo` fast path -> the remaining chunk-to-caller copy was only 0.8% of integrated CPU samples, too little to justify a complex Close, short-write, progress, and digest lifecycle.
- Keep Mount-to-Push session reuse out of this change -> safely handing an upload session between separate calls needs an explicit API; implicit caching would leak sessions and create ambiguous concurrent ordering.
- Put public performance regressions and benchmarks in `internal/perf`, while retaining white-box resource invariants beside production code -> this keeps the root cleaner without exposing internals or relying on reflection.

## Changes

- `upload_body.go` and `push.go` - added ownership-aware bounded upload staging, proportional small-body allocation, and a hard 2 MiB process-wide idle-buffer cap.
- `parallel.go` and `client.go` - added fixed workers, ordered reassembly, direct ranged-body reads, bounded buffer retention, tracked response-body cancellation, bounded range fragmentation, and suitable default connection pools.
- `internal/perf/` - centralized Pull and Push benchmarks plus public batching, streaming, and HTTP/1 reuse regressions.
- `parallel_correctness_test.go`, `parallel_internal_test.go`, `upload_buffer_test.go`, and `transport_test.go` - added deterministic lifecycle, amplification, memory-bound, and transport-ownership coverage.
- `docs/docs/explanation/design.md` - documented bounded transfer buffering and default transport behavior.

## Open Threads

- A future combined Mount-or-Push API could reuse a declined mount's upload session; separate `Mount` and `Push` calls cannot safely do so implicitly.
- Pull becomes CPU-limited above roughly 17 Gbit/s on the measured host. Re-profile SHA-256 and scheduling before optimizing further; the remaining memory copy is not currently material.
- Historical root-package benchmark files need `benchstat -ignore pkg` when compared with results from `internal/perf`.

## References

- PR #21: https://github.com/imgoci/go-oci-blob/pull/21
- Merge commit: `0251e8cead2021022fbfc1b188eeeff1030172cc`
- PR #20 correctness baseline: https://github.com/imgoci/go-oci-blob/pull/20
- Authoritative design: `docs/docs/explanation/design.md`
- Prior implementation and hardening: `.journal/002/SUMMARY.md`, `.journal/003/SUMMARY.md`

## Lessons

- A fast loopback benchmark does not prove a transfer is network-limited; controlled bandwidth caps plus CPU and memory profiles establish the boundary.
- Benchmark order and host temperature can swamp small pull deltas. Balanced ABBA samples and `benchstat` separated real allocation wins from noise.
- A locally managed `sync.Pool` is not a memory-retention bound; bounded channels provide deterministic cache capacity while preserving warm reuse.

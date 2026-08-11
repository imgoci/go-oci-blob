---
id: 001
title: Design doc for go-oci-blob
started: 2026-08-10
---

## 2026-08-10 19:10 — Kickoff
Goal for the session: produce a proper design doc for go-oci-blob before touching any code in the repo.
Current state of the world: repo `imgoci/go-oci-blob` was just created from `meigma/template-go` (private, default branch `master`) and cloned to `~/code/imgoci/go-oci-blob`. Session journal was initialized on `journal/jmgilman`. The template rename pass has not been done: module is still `github.com/meigma/template-go`, `cmd/template-go` is unrenamed, and `DELETE_ME.md` still exists.
Plan: gather requirements from the user on what go-oci-blob should do, draft a design doc (hexagonal architecture per repo rules), iterate with the user, and land it before implementation starts.

## 2026-08-10 19:14 — Scope + stdlib feasibility
Library premise settled: go-oci-blob does exactly one thing — push/pull OCI blobs. No manifests, tags, signatures, or wider OCI spec. Tolerant handling of registry responses, heavy optimization, retries, hash verification. Zealous minimalism: every line must justify itself.
Answered stdlib question: yes, runtime can be pure stdlib. net/http covers the five request shapes (HEAD/GET/POST/PATCH/PUT), redirects, Location resolution; encoding/json for the OCI error body; crypto/sha256 for verification; context/time/math/rand for retries. Auth is out of scope — consumer injects an authenticated http.RoundTripper (ORAS/go-containerregistry provide these). Go's cross-host Authorization stripping on redirects is the correct CDN behavior; document as intentional.
Open question: take opencontainers/go-digest for API interop, or define our own Digest type (~50 lines)? Sole runtime-dependency debate. Test-only deps (mockery, testcontainers) excluded from the zero-dep claim.
Next: user decides go-digest question, then draft design doc.

## 2026-08-10 19:22 — Initial design doc drafted
User confirmed: go-digest accepted as sole runtime dep; auth stays out via injected transport.
Drafted docs/docs/explanation/design.md on branch docs/initial-design (worktree .wt/docs-initial-design), added mkdocs nav entry, opened PR #7.
Design highlights: single `blob` package; http.RoundTripper is the hexagonal port (auth + test seam in one); Pull returns verifying io.ReadCloser (ErrDigestMismatch at EOF); Push takes digest+size, monolithic-first with chunked fallback; Mount returns (false, nil) on decline, caller decides fallback; retry on conn errors/429/5xx with full jitter + Retry-After; ranged resume for pulls, Range-ack resume for uploads; sentinels limited to ErrNotFound + ErrDigestMismatch.
Open questions parked in doc: size -1 push, PullRange, progress callbacks.
Next: user reviews PR #7; iterate on doc, then merge before implementation.

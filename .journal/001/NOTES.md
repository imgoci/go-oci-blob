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

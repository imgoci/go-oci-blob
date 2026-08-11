---
id: 001
title: Design doc for go-oci-blob
date: 2026-08-10
status: complete
repos_touched: [go-oci-blob]
related_sessions: []
---

## Goal

Produce a design document for go-oci-blob before writing any code. Scope grew
by user request to include reshaping the freshly templated repo into its
library-only form and writing a phased implementation plan.

## Outcome

Goal met, plus the bootstrap work. The design doc is merged and
decision-complete (no open questions). The repo is in its target shape:
library-only layout, module `github.com/imgoci/go-oci-blob`, zero runtime Go
dependencies, dual Apache-2.0/MIT license, public, settings and rulesets
applied, docs on GitHub Pages, security alerts remediated, zero open PRs.
An 8-phase implementation plan is at `.journal/001/PLAN.md`.

## Key Decisions

- Pure stdlib runtime plus `opencontainers/go-digest` only -> go-digest taken
  for API interop with ORAS/go-containerregistry, not function.
- Auth out of scope -> caller injects an authenticated `http.RoundTripper`,
  which doubles as the hexagonal port and the mockery seam.
- Defaults are the universally supported paths (monolithic upload,
  single-stream pull); chunked upload and parallel pull are explicit toggles,
  never fallbacks -> hosted registries silently break chunked upload (ECR
  discards chunks after the first; Docker Hub/GHCR reports similar) because
  no mainstream client exercises it.
- Upload session resume cut from v1 (restart from zero); chunked mode must
  verify the `Range` ack advances after every `PATCH`.
- `Pull` returns a digest-verifying `io.ReadCloser` (`ErrDigestMismatch` at
  EOF); `PullRange` is deliberately unverified (digest covers the whole
  blob); `Push` size is mandatory (no size -1); progress is a per-call
  `TransferOption` because caller-side reader wrapping double-counts on
  upload restart.
- Dual license Apache-2.0 OR MIT (user choice); Release Please kept for
  semver tags with the imgoci app.

## Changes

- `docs/docs/explanation/design.md` — the design document (PR #7, refined in
  #8's base by two follow-up commits before merge).
- Library-only reshape (PR #8): deleted CLI (`cmd/`, `internal/`), GoReleaser,
  ghd, melange/apko, attest/security-scan/release workflows and their mise
  tools/skills; renamed module with placeholder root `package blob` (doc.go);
  Release Please reset (manifest 0.0.0, changelog cleared, IMGOCI_* creds);
  README/docs/moon/mise/golangci rewritten; LICENSE-APACHE + LICENSE-MIT.
- `docs/uv.lock` — pymdown-extensions 11.0.1 security bump (PR #9), clearing
  both Dependabot alerts.
- Out-of-band (not in git): repo made public by user; settings applied via
  `configure_github_repo.py` (rulesets, Pages, security toggles); release app
  var/secret set from 1Password; Dependabot PRs #1-#4 closed as obsolete,
  #5/#6 merged.

## Open Threads

- Confirm the `imgoci-release-please` GitHub App is installed on the repo
  before the first release (org-admin action; could not verify from CLI).
- A few settings are GitHub-UI-only (Archive Program, automatic dependency
  submission, Dependabot malware alerts); listed under `[unsupported]` in
  `.github/repository-settings.toml`.
- Implementation has not started. Next session should begin at Phase 1 of
  `.journal/001/PLAN.md` (skeleton + Exists + test scaffolding).

## References

- PRs: #7 (design doc), #8 (library reshape), #9 (pymdown bump);
  Dependabot #5/#6 merged, #1-#4 closed.
- Design doc: `docs/docs/explanation/design.md` (authoritative).
- Implementation plan: `.journal/001/PLAN.md`.
- Registry breakage evidence: aws/containers-roadmap#2831 (ECR chunked
  upload), aahlenst.dev "Storing Blobs on the GitHub Container Registry".

## Lessons

- Spec-compliant is not registry-supported: upload session resume is
  normative in distribution-spec v1.1 yet broken or untested on every major
  hosted registry. Verify wire features against real registries before
  designing reliability on top of them.

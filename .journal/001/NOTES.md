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

## 2026-08-10 19:28 — Upload resume reality check
User challenged the upload-resume claim. Findings: GET-on-upload-URL resume is normative in distribution-spec v1.1 (204 + Range, in-order chunks, 416 recovery) but chunked upload itself is optional, and hosted support is broken: ECR silently discards PATCH bodies after the first chunk (aws/containers-roadmap#2831, Range ack never advances), field reports say chunked is broken on Docker Hub and GHCR. No mainstream client (docker/containerd/oras) uses chunked or resume; they push monolithically and restart, so the path rots. registry:2 and zot implement it correctly.
Proposed doc changes (awaiting user): (1) demote upload resume to opportunistic or cut from v1, keep monolithic-first + restart; (2) add wire rule to verify Range ack advances after every PATCH and abandon session on mismatch (digest-verified commit PUT is the backstop). Download resume via ranged GET stands, gated on Accept-Ranges/206.

## 2026-08-10 19:34 — Toggles decided
User decisions: (1) chunked upload stays but as an explicit toggle (WithChunkedUpload), never a fallback; default remains monolithic PUT. (2) Parallel ranged pull is promoted to the library's one built-in extra, behind WithParallelPull(workers, chunkSize), default single stream.
Doc updated on PR #7 (commit b0adb56): defaults-are-universal-paths principle added to API decisions; chunked mode verifies Range ack after every PATCH and fails on non-advancing ack; upload session resume cut from v1 (uploads restart from zero, needs re-readable reader); parallel pull keeps the Pull signature, emits in order through the verifying reader, memory bounded by workers×chunkSize, falls back to single stream without range support; scatter-write WriterAt variant explicitly rejected; size -1 push open question now tied to chunked toggle.
Next: user review of PR #7.

## 2026-08-10 19:40 — Open questions resolved
User rulings: no size -1 (Push size mandatory, callers spool if unknown); PullRange is in (trivial, real value); progress callbacks are in (guaranteed first feature request, consumers need visibility).
Doc updated (commit 0b51490): PullRange added to API sketch as unverified ranged read (digest covers whole blob, partial body can't be checked; Pull remains the verified path); progress as per-call TransferOption WithProgress(fn(done, total)) with committed-progress semantics — rationale recorded that caller-side reader wrapping double-counts on upload restart; parallel pull reports one aggregated count. Open questions section removed; all questions resolved into the body.
Design doc is now decision-complete. Next: user approval/merge of PR #7, then implementation planning.

## 2026-08-10 20:06 — Design merged; repo reshaped to library-only
Merged PR #7 (design doc) via squash. Then followed DELETE_ME.md's library-only checklist on branch chore/library-shape (PR #8, 43 files, -3015 lines):
- Deleted: cmd/, internal/, goreleaser, ghd.toml, melange/apko yaml + skills + mise tools/task, release/release-dry-run/attest/security-scan workflows, ghd staging scripts, DELETE_ME.md.
- Module renamed to github.com/imgoci/go-oci-blob; go.mod now zero deps; placeholder root package blob (doc.go) keeps build/lint/test green.
- Kept Release Please per user: manifest reset 0.0.0, CHANGELOG cleared, workflow now uses IMGOCI_RELEASE_APP_ID / IMGOCI_RELEASE_APP_PRIVATE_KEY. Creds sourced from 1Password item imgoci-release-please (Development vault, fields app_id + key.pem file) and set on the repo via gh variable/secret set.
- repository-settings.toml: is_template=false, required checks -> ci only, tag bypass app -> imgoci-release-please. NOTE: settings not yet applied to GitHub; run .github/scripts/configure_github_repo.py apply after merge.
- User decisions this turn: dual license Apache-2.0 OR MIT (LICENSE-APACHE + LICENSE-MIT, Rust-style); keep Release Please.
- moon run root:check passes; mise.lock + docs/uv.lock regenerated; rg shows zero template-go/meigma refs left.
- .claude/skills is a symlink to .agents/skills in the main checkout, so deleted skills disappear on merge automatically.
Pending: PR #8 CI, then merge; obsolete dependabot PRs (goreleaser-action, docker/login-action, attest) should auto-close after merge; apply repository settings; release app must be installed on imgoci/go-oci-blob for release-please to work.

## 2026-08-10 20:15 — Repository settings applied
User made the repo public (rulesets + Pages need public on free plan; first apply attempt 403'd on rulesets while private). Ran .github/scripts/configure_github_repo.py apply --repo imgoci/go-oci-blob from the chore/library-shape worktree (updated TOML): general settings, immutable releases, private vuln reporting, automated security fixes, Pages (workflow build, HTTPS, https://imgoci.github.io/go-oci-blob/), and both managed rulesets created. Re-plan converges: no supported changes required. Remaining items are GitHub-UI-only toggles the script lists as manual follow-ups (Archive Program, dependency submission, malware alerts, etc.).

## 2026-08-10 20:20 — PR #8 merged
Squash-merged chore/library-shape (master b60c246); main checkout fast-forwarded; worktree removed. Closed obsolete dependabot PRs #1 (goreleaser-action), #2 (attest), #3 (docker/login-action) — their workflows were deleted. PRs #4-6 (setup-go, mise-action, mkdocs-material) remain valid for review. Master CI + Pages deploy running post-merge. Repo reshape is complete: design doc merged, library-only layout, settings applied, release creds set, public visibility, Pages live at https://imgoci.github.io/go-oci-blob/ once deploy finishes.

## 2026-08-10 20:28 — Dependabot PRs addressed
#4 (setup-go) closed — touched only deleted release workflows (dependabot had already closed it). #5 (mise-action 4.2.4) and #6 (mkdocs-material 9.7.7) squash-merged after confirming dependabot had rebased them onto the renamed project (checked #6's uv.lock for stale template-go-docs name: clean). Rebased my #9 (pymdown-extensions 11.0.1 security bump) onto the new master by restoring master's uv.lock and re-running uv lock --upgrade-package; branch force-pushed, docs build green. #9 merge closes both open Dependabot alerts.

## 2026-08-10 20:34 — All PRs merged; repo clean
Squash-merged #9 (pymdown-extensions 11.0.1 + carries mkdocs-material 9.7.7). Zero open PRs. Dependabot alerts pending async graph rescan; master lock confirmed at 11.0.1. Repo state: design doc merged, library-only shape, settings/rulesets applied, public, Pages live, release creds set, security alerts remediated. Session goal (design doc before code) plus full repo bootstrap complete. Nothing in flight except confirming imgoci-release-please app installation before first release.

## 2026-08-10 20:40 — Implementation plan written; alerts cleared
Dependabot alerts confirmed resolved (0 open) after the graph rescan picked up pymdown-extensions 11.0.1.
Wrote .journal/001/PLAN.md: 8-phase implementation plan for the merged design. Phase order: (1) skeleton+Exists with full test scaffolding (mockery, testcontainers e2e vs registry:2+zot), (2) Pull/PullRange+verifying reader, (3) Push monolithic+Mount, (4) reliability (retry policy, ranged resume, restart), (5) chunked upload toggle w/ Range-ack verification, (6) parallel pull toggle+benchmarks, (7) progress callbacks, (8) Diátaxis docs+v0.1.0 release. One PR per phase, each leaves master releasable; standing rules: design doc updated on drift (D6), no runtime deps beyond stdlib+go-digest, revise design first if a prototype falsifies it.

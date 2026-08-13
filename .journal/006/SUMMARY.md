---
id: 006
title: User documentation set and tabbed compatibility matrix
date: 2026-08-12
status: complete
repos_touched: [go-oci-blob]
related_sessions: [001, 005]
---

## Goal

Create the project's user-facing documentation following Diátaxis and the
plain-language style skill, preferring few high-quality documents over a broad
corpus, then iterate the registry compatibility reference into a cleaner
presentation under user review.

## Outcome

Goal met. PR #26 added the documentation set and was squash-merged as
`14c5063`: one tutorial (push/exists/pull against a local registry), two
how-tos (authentication, chunked upload and parallel pull), the registry
compatibility reference, a rewritten docs landing page, a language pass on the
design explanation, and a README refresh that removed the stale
"public API does not exist yet" claim. PR #27 then reworked the compatibility
reference under per-change user review and was squash-merged as `7453b75`:
per-registry content tabs, full-width tables inside tabs, colored
check/cross/minus result icons, per-feature hover tooltips, and the
per-registry notes folded into a compact "Differences at a glance" section.
All post-merge CI, Code Quality, CodeQL, Release Please, and Pages runs
passed; the site is live.

## Key Decisions

- Keep the API reference in godoc/pkg.go.dev only -> duplicating it on the
  docs site creates a second source of truth that drifts; the site links out.
- Verify documentation by execution, not review -> the tutorial ran verbatim
  through public `go get` plus a Docker `registry:2` (both expected-output
  blocks are pasted from the real run, which caught a fabricated digest), all
  how-to snippets compiled and vetted verbatim against published dependency
  versions, and the oras-go auth recipe was runtime-proven against an
  htpasswd-protected registry.
- Reuse the compatibility harness's auth pattern in the how-to -> the
  `auth.Client` adapter with a redirect-disabled inner client is the exact
  construction verified against nine registries in session 005.
- Publish the compatibility matrix as a dated observation set -> each page
  carries the campaign date, library commit, and tool versions, and the
  refresh runbook is linked so the matrix cannot silently rot into product
  claims.
- Present the matrix as per-registry tabs with a divergence summary -> nine
  narrow tables read better than one 10-column grid, and the summary section
  preserves cross-registry comparison the tabs would otherwise hide.
- Move feature definitions into hover tooltips and delete per-tab notes -> the
  user chose compactness; the load-bearing failure mechanisms moved into
  "Differences at a glance" so no verified fact was lost.

## Changes

- `docs/docs/tutorials/getting-started.md` - new tutorial; outputs from a real
  run.
- `docs/docs/how-to/authenticate.md` - new; oras-go adapter and
  go-containerregistry transport recipes plus off-origin transport guidance.
- `docs/docs/how-to/tune-transfers.md` - new; opt-in chunked/parallel options
  with ledger-grounded registry caveats.
- `docs/docs/reference/registry-compatibility.md` - new, then reworked into
  tabs, icons, and tooltips; data transcribed from
  `.journal/005/REGISTRY_TEST_RESULTS.md` and DOM-diffed (180 cells, zero
  mismatches) after each rework.
- `docs/docs/index.md`, `docs/docs/explanation/design.md`, `README.md` -
  landing page rewritten as a doc map; design doc de-staled (implemented
  present tense, internal rule IDs removed); README updated with install,
  example, and doc links.
- `docs/mkdocs.yml`, `docs/docs/stylesheets/palette.css` - nav for all four
  Diátaxis sections; `pymdownx.tabbed`, `pymdownx.emoji`, `content.tooltips`;
  width overrides for tables inside tabs and result-icon colors.

## Open Threads

- Feature definitions in the compatibility matrix exist only as hover
  tooltips, which touch devices cannot reach; a collapsible glossary via
  `pymdownx.details` was offered and not requested.
- Tutorial and how-to expected outputs embed real library error strings and a
  content digest; re-run them when Pull/Push error wording or the tutorial
  payload changes.
- Phase 8's remaining half — the first tagged release — is still open; no
  version tag exists yet.

## References

- Documentation set: https://github.com/imgoci/go-oci-blob/pull/26
- Tabbed matrix rework: https://github.com/imgoci/go-oci-blob/pull/27
- Live reference: https://imgoci.github.io/go-oci-blob/reference/registry-compatibility/
- Compatibility evidence ledger: `.journal/005/REGISTRY_TEST_RESULTS.md`
- Design doc and docs conventions: `docs/docs/explanation/design.md`

## Lessons

- Executing documentation is the only reliable review: the tutorial's original
  expected digest was plausible and wrong, and only the verbatim run exposed
  it.
- Material for MkDocs shrinks tables twice — the `<table>` and its
  `.md-typeset__table` wrapper are both `inline-block` — so full-width tables
  need overrides on both, scoped to avoid stretching small metadata tables.
- Rendering a rework from parsed source data and DOM-diffing the result
  catches transcription drift cheaply; retyping 180 cells would have invited
  it.

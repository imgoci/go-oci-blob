---
id: 002
title: Begin implementation per session 001 plan
started: 2026-08-10
---

## 2026-08-10 20:27 — Kickoff
Goal for the session: start implementing go-oci-blob per the phased plan from
session 001 (`.journal/001/PLAN.md`), beginning at Phase 1 (skeleton + Exists +
test scaffolding).
Current state of the world: repo is in library-only shape (module
`github.com/imgoci/go-oci-blob`, placeholder root `package blob`), the design
doc at `docs/docs/explanation/design.md` is merged and decision-complete, repo
settings/rulesets/Pages applied, Release Please wired with the imgoci app
credentials, zero open PRs, master clean at 0db17bf. No implementation code
exists yet.
Plan: read `.journal/001/PLAN.md` and the design doc, create an implementation
worktree from master, and work through Phase 1 with tests before moving on.

## 2026-08-10 20:51 — Phase 1 implemented, PR #10 open
Done: full Phase 1 in worktree `feat/phase1-skeleton-exists` — client.go
(Client/New/WithTransport/WithPlainHTTP), repo.go (Repository + OCI name
grammar validation via allocation-free scanner), wire.go (pure core: blob URL,
Location resolution, status families, OCI error body parse with fallback,
404→ErrNotFound), errors.go, exists.go (HEAD; 2xx→true, 404→(false,nil)).
Scaffolding: testify, .mockery.yml (mockery 3.7.2 pinned in mise via aqua,
locked all 4 platforms), mocks/RoundTripper generated, unit + mocked-transport
integration tests, e2e harness (testcontainers, registry:2 + zot v2.1.20,
skip under -short, blobs seeded via raw HTTP POST+PUT until Phase 3 Push).
All green locally: lint, fmt, build, go test -race incl. e2e both registries.
PR: https://github.com/imgoci/go-oci-blob/pull/10 (awaiting CI).

Learned (durable, promote to TECH_NOTES at close): `moon run` is broken
locally on machines with a legacy ~/.proto store — moon v2 unconditionally
prepends ~/.proto/tools/proto/<ver>, ~/.proto/shims, ~/.proto/bin to task
PATH, so proto's go 1.26.5 shadows mise's pinned 1.26.4 and mise's exported
GOROOT causes "compile: version go1.26.4 does not match go tool version
go1.26.5". CI unaffected (no ~/.proto on runners). No moon config toggle
found. Workaround: run task commands directly via `mise exec --`. Decision
needed (likely template-go level): clean stale proto store vs .prototools
mirror pins vs moon.yml `mise exec` wrappers.

Next: watch PR #10 CI, merge (squash) when green, then Phase 2 (Pull/
PullRange + verifying reader).

## 2026-08-10 21:00 — E2E tests moved behind a build tag (user request)
e2e_test.go now carries `//go:build e2e`; the -short skip is gone. Plain
`go test ./...` is fast and Docker-free. New moon task root:test-e2e runs
`go test -tags e2e ./...` and is a dep of root:check, so CI still exercises
e2e (verified in the CI log: root:test-e2e completed, 11s). .golangci.yml
gained run.build-tags [e2e] so the tagged file stays linted. Convention for
later phases: all e2e tests go in e2e-tagged files.

## 2026-08-10 21:25 — Phase 1 merged; Phase 2 implemented, PR #12 open
PR #10 squash-merged (93dc58c); phase-1 worktree removed. Phase 2 in worktree
`feat/phase2-pull`: verify.go (verifyReader hashes as bytes flow, swaps EOF
for ErrDigestMismatch), pull.go (Pull → verifying ReadCloser; PullRange
unverified per design, Range header, 206 pass-through, 200 ignored-range
fallback carves the window via discard+LimitReader; shared c.get helper),
transfer.go (empty TransferOption so signatures are final), ErrDigestMismatch
sentinel, validateTarget refactor shared with Exists. Tests: verifyReader
units (incl. iotest), scripted-transport integration (redirects abs+rel,
garbage error bodies, 404→ErrNotFound, range paths), e2e pull + range
round-trips green on registry:2 + zot with -race.
PR: https://github.com/imgoci/go-oci-blob/pull/12 (CI in flight).

Gotchas hit: (1) golangci-lint's shared cache replayed findings for the
deleted phase-1 worktree path — the generated-file filter can't re-read the
deleted file so previously-filtered mockery-mock findings leaked through with
../feat-phase1-* paths; `golangci-lint cache clean` fixed it. (2) bodyclose
flags Pull's escaping response body (verifying reader owns it) — narrow
nolint with explanation. (3) Note: release-please holds PR #11.

---
id: 001
title: Design doc for go-oci-blob
started: 2026-08-10
---

## 2026-08-10 19:10 — Kickoff
Goal for the session: produce a proper design doc for go-oci-blob before touching any code in the repo.
Current state of the world: repo `imgoci/go-oci-blob` was just created from `meigma/template-go` (private, default branch `master`) and cloned to `~/code/imgoci/go-oci-blob`. Session journal was initialized on `journal/jmgilman`. The template rename pass has not been done: module is still `github.com/meigma/template-go`, `cmd/template-go` is unrenamed, and `DELETE_ME.md` still exists.
Plan: gather requirements from the user on what go-oci-blob should do, draft a design doc (hexagonal architecture per repo rules), iterate with the user, and land it before implementation starts.

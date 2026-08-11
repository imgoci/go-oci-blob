# Technical Notes

- Use hexagonal architecture at all times. Keep business logic isolated from CLI, filesystem, network, storage, and other external adapters.
- Prefer functional testing before calling any feature complete. Unit tests are useful, but they do not prove the tool works the way the design intends.
- Take an agile approach to development. Avoid waterfall: underspecify when useful, prototype early, learn from the result, and refine from working behavior.
- `docs/docs/explanation/design.md` is the authoritative design; update it in the same PR when implementation forces an unrecorded decision. The phased implementation plan is `.journal/001/PLAN.md`.
- Release Please auth: repo variable `IMGOCI_RELEASE_APP_ID` + secret `IMGOCI_RELEASE_APP_PRIVATE_KEY`, sourced from 1Password item `imgoci-release-please` (Development vault, `op` CLI; app_id field + key.pem file). Confirm the app is installed on the repo before the first release.
- Repository settings are code: `.github/repository-settings.toml`, applied with `uv run .github/scripts/configure_github_repo.py apply --repo imgoci/go-oci-blob` (applied 2026-08-10; a few toggles are GitHub-UI-only, see `[unsupported]`).
- Hosted registries (ECR, Docker Hub, GHCR) ship broken chunked blob upload/resume because mainstream clients never use it; never make a spec'd-but-unexercised registry feature a default path without testing against real registries.

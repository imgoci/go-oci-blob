---
id: 005
title: Start a new work session
started: 2026-08-11
---

## 2026-08-11 16:22 — Kickoff

Goal for the session: Start a fresh journal session and await the user's project request.
Current state of the world: Sessions 001 through 004 are closed; the blob transfer implementation, correctness hardening, and verified performance fixes are merged, while the next project task has not yet been stated.
Plan: Bind session 005, receive the user's scoped request, and record meaningful checkpoints as the work progresses.

## 2026-08-11 17:53 — Manual functional campaign planned

Goal: Propose a thorough, non-permanent manual functional test of every real consumer-facing library promise, then pause for review before executing it.
Current state: The exact target is clean `master` at `1bc039c48092b1a29eda6dcf678a06fd87468a4b`. The repository has 95 top-level tests across 23 test files, but its live E2E layer is limited to unauthenticated plain HTTP against Registry 2 and Zot with roughly 1 MiB payloads.
Plan: Export the exact Git tree to a read-only temporary snapshot; compile a disposable external consumer module; run the unchanged repository corpus as calibration; then exercise real registries, a real-socket fault laboratory, shared-client race/concurrency stress, cancellation and cleanup, origin credential boundaries, large-stream resource bounds, and independent digest verification. Keep all harness code and evidence outside the repository, create no project branch or PR, and verify the worktree remains unchanged.
High-risk probes: Measure the literal `RetryPolicy.MaxAttempts` boundary across parallel chunks and PullRange continuations, and test whether one reused progress callback can overlap across concurrent transfers despite the unqualified serialization wording.
Next: Await user review of the proposed campaign before creating any harness, container, VM, or hosted repository.

## 2026-08-12 01:47 — Consumer campaign executed

Target and isolation: Exported commit `1bc039c48092b1a29eda6dcf678a06fd87468a4b` (tree `a530b8b10212449ccef2becb8ea434cf10791cda`) to a read-only temporary source tree and built a separate disposable Go consumer module. The project checkout remained clean and no library files were changed.

Passing evidence: The unchanged build, unit, race, Registry 2/Zot E2E, E2E-race, and benchmark calibration all passed. External consumer tests passed against Registry 2, Zot, a TLS plus Basic-auth Registry 2, GHCR, real-socket truncation/resume, retry/status faults, exact range continuations, integrity failures, redirects and credential stripping, monolithic/chunked upload protocols, mount cleanup, exact reader sizes, cancellation, shared-client mixed-operation stress, large streaming transfers, connection reuse, and resource cleanup. The shared-client race campaign completed 648 mixed operations across `GOMAXPROCS=1,2,8` without a race, deadlock, or data mix-up. Large 64 MiB serial/parallel Pull and non-seekable Push measurements stayed far below payload size and reused bounded connections.

Contract findings: `RetryPolicy.MaxAttempts` says one operation has one total attempt budget, while the parallel-pull design and tests apply the budget per logical range request. A parallel Pull configured for two attempts made seven wire requests across four ranges while every range stayed within two attempts; this is a documentation ambiguity, not a proven reliability failure. `WithProgress` says callbacks never run concurrently with themselves, but reusing one callback across two concurrent transfers caused overlap because serialization is per transfer. The callback probe is retained as intentionally failing literal-contract evidence rather than a green characterization test.

Hosted characterization: GHCR accepted an unreferenced blob with `201` and answered HEAD as present but returned `BLOB_UNKNOWN` to GET; an ORAS blob-only control behaved the same. After ORAS linked the same bytes through a minimal manifest, independent ORAS fetch plus library serial and parallel Pull all verified the exact bytes. Every uniquely named GHCR package was deleted and confirmed absent.

Next: Finish the aggregate normal/race reruns after final harness review, verify all temporary processes/containers/hosted packages are gone, record artifact hashes and source immutability, then report the pass/fail matrix without merging any test code.

## 2026-08-12 02:02 — Final evidence and cleanup audit

Aggregate result: The final positive consumer suite passed 32 top-level tests and 118 test nodes with zero skips or failures. A focused race run passed 23 protocol/fault/TLS top-level tests and 59 nodes. The resource lane passed three normal repetitions plus race; the concurrency lane passed 648 mixed operations across `GOMAXPROCS=1,2,8` plus a revised three-repeat race check. The strict cross-transfer callback probe failed reproducibly in all three repetitions, confirming that the same callback can overlap when reused by concurrent transfers even though ordinary per-transfer callback serialization passed.

Final classifications: No functional data-integrity, retry, streaming, shared-client race, cleanup, TLS/auth, Registry 2, Zot, or GHCR lifecycle defect was found. The remaining actionable contract issue is the unqualified `WithProgress` serialization wording. The parallel retry wording is ambiguous between operation and logical-request scopes. The design's advertised-range gate for broken-stream resume is stale in a reliability-positive direction: the implementation resumes without the advertisement and safely accepts a full-200 fallback. The README's claim that no public API exists is also stale.

Cleanup and proof: A fresh `git archive` diff against the temporary source was empty; the source has zero user-writable files; the real checkout is still clean at the same commit and tree. All current-run Registry 2, Zot, TLS registry, and Ryuk containers were removed. The only Docker resources left are one unrelated running GitHub MCP container and two unrelated stale Aug 8 resources that predated the campaign. No uniquely named GHCR functional package remains. Harness/evidence checksums are in `/tmp/go-oci-blob-functional.quDtZu/evidence/SHA256SUMS`; the resource report is `resource-lane.md`.

Next: Deliver the final manual functional-test verdict. No test harness or library change will be merged.

## 2026-08-12 10:04 — Progress callback contract fix published

Decision: Fix the functional campaign's one actionable contract finding by clarifying the API rather than imposing global callback serialization. `WithProgress` now states that callbacks do not overlap within one transfer, while concurrent transfers may invoke a reused callback at the same time and therefore require caller-side synchronization of shared state. The design explanation carries the same boundary.

Test change: Made the public progress recorder race-safe and added a common assertion that callback invocations observed within each tested transfer do not overlap. Kept the test at the public API boundary; an implementation-coupled tracker concurrency test had intentionally been removed in PR #23. Did not add a test requiring cross-transfer overlap because the clarified contract permits overlap without promising it.

Verification: Focused progress tests passed 20 normal repetitions and five race repetitions. The full build, unit suite, race suite, fresh-cache golangci-lint format/lint checks, strict MkDocs build, and tagged Registry 2/Zot E2E suite all passed. The initial lint invocation read stale cache paths from a deleted sibling worktree; rerunning with an isolated fresh cache passed cleanly.

Published: Commit `2d238b6` (`fix: clarify progress callback concurrency contract`) on `feat/progress-callback-contract`; PR #24: https://github.com/imgoci/go-oci-blob/pull/24.

Next: Await review and hosted checks. Do not merge or close session 005 without explicit user direction.

## 2026-08-12 10:05 — Hosted checks green

PR #24 passed CI, both Go and Actions CodeQL analyses, and the GitHub Pages validation. The PR remains open and unmerged for user review.

## 2026-08-12 10:09 — Progress callback contract fix merged

Merged PR #24 through GitHub with a squash merge pinned to reviewed head `2d238b667d555070484c5da1d5bb4e26b55f631b`. `master` now contains `8700a0989bb82ca272ca986e2dc8eae79536d1b5` (`fix: clarify progress callback concurrency contract (#24)`). The remote feature branch, clean local feature worktree, local feature branch, and disposable docs build environment were removed after Worktrunk confirmed the branch tree matched `master`.

Post-merge verification: CI, both CodeQL workflows, Release Please, and GitHub Pages all passed on the merged commit. The main worktree is clean and synchronized with `origin/master`.

Next: Keep session 005 open for further user direction.

## 2026-08-12 11:02 — Amazon ECR compatibility campaign

Target and isolation: Tested Amazon ECR Private in account `803789966077`, region `us-east-2`, from a disposable external consumer against a read-only export of merged commit `8700a0989bb82ca272ca986e2dc8eae79536d1b5`. Go was `1.26.4 darwin/arm64`; ORAS was `1.3.0+unreleased`. The project checkout remained clean and no library file was changed.

Passing capabilities: HTTPS and token authentication, independently seeded small and 8 MiB blobs, present/missing `Exists`, serial Pull, progress, bounded `PullRange`, native ranged parallel Pull with exactly four simultaneous response bodies, interrupted-stream resume, unreferenced blob retrieval, monolithic Push, wrong-digest and exact-size rejection, off-origin redirect credential scoping, opaque same-origin upload Locations, and a 23-operation shared-client concurrency campaign all passed. The concurrency run completed under the race detector in 21.83 seconds, independently verified four uploaded artifacts, left no ranged bodies active, observed no retryable status, and reported no race.

Compatibility limits: Empty-blob Push failed twice, including once in a fresh repository: ECR opened the upload with `202` and then rejected the zero-byte final `PUT` with `400 BLOB_UPLOAD_INVALID` because no parts existed. Chunked Push failed reproducibly with fresh digests at the configured 1 MiB setting and a 5 MiB diagnostic setting. ECR advertised `OCI-Chunk-Min-Length: 10485760`, accepted the resulting 8–10 MiB `PATCH` body with `201 Created` instead of a continuation response, and did not make the blob available; the library rejected the response and attempted cleanup. Cross-repository Mount remains uncharacterized because the regional `BLOB_MOUNTING` account setting is `DISABLED`; the observed `202` decline cleanly mapped to `false, nil`, so the row is blocked by configuration rather than unsupported.

Wire and cleanup proof: ECR redirected blob reads with `307` to off-origin storage, which returned native `200`, `206`, and past-end `416` responses. No registry credential, Cookie/Cookie2, proxy authorization, or Referer reached off-origin storage, and no transport-role mismatch or unredacted signed query was retained. The full race-enabled campaign took 95.41 seconds. Evidence SHA-256 was `3a696b290802c016c47d91d89bb00ba4254e344d6dc070d9d339b3c39f67eb9a`; the fresh-repository empty-repeat evidence hash was `4f5f1d6e479494baa17bcfdcc4540ced37f6cdfcb536e4e4aa481999c736adac`. All three uniquely tagged repositories were force-deleted and confirmed absent; `BLOB_MOUNTING` remained disabled.

Next: Report the ECR feature matrix and preserve the registry-neutral test procedure for the next candidate. Do not merge the disposable harness or evidence.

## 2026-08-12 11:18 — ECR blob mounting enabled and verified

Account change: With explicit user authorization, changed the Amazon ECR Private regional account setting `BLOB_MOUNTING` from `DISABLED` to `ENABLED` in account `803789966077`, region `us-east-2`. Immediate readback returned `ENABLED`; the setting intentionally remains enabled after the campaign.

Focused result: PASS. A disposable race-enabled external consumer independently seeded a unique 2,097,473-byte layer into a fresh AES-256 source repository, proved the fresh AES-256 destination lacked it, and called the public `Mount` method. ECR returned `201 Created` with the expected `Docker-Content-Digest`; the library returned `(true, nil)`; no PATCH, PUT, or cleanup DELETE occurred on the successful path. The destination then passed library `Exists`, ECR `BatchCheckLayerAvailability`, exact raw GET plus independent SHA-256, and exact ORAS fetch. The destination had zero manifests, proving the layer arrived through Mount rather than an independent push, and the source remained readable.

Negative control and cleanup: A fresh nonexistent source digest returned `202 Accepted`; the library returned `(false, nil)`, attempted DELETE of the opened session, and the destination stayed absent. The test passed under Go's race detector in 11.74 seconds. Both uniquely tagged repositories were force-deleted and independently confirmed absent; the final matching repository count was zero. Evidence SHA-256 was `28c589f4711c8d18b4e318476bfaebfd2cf17f93b15284a6fa38673c441c68f7`. The disposable harness changed no library code and the main worktree remained clean at `8700a0989bb82ca272ca986e2dc8eae79536d1b5`.

Matrix correction: Amazon ECR cross-repository Mount is `PASS` when the regional `BLOB_MOUNTING` setting is enabled and the documented same-account, same-region, matching-encryption prerequisites hold. The earlier `BLOCKED` classification applied only to the then-disabled account configuration.

## 2026-08-12 12:01 — GHCR compatibility campaign

Target and isolation: Tested `ghcr.io` as user `jmgilman` from a disposable external consumer against a read-only export of merged commit `8700a0989bb82ca272ca986e2dc8eae79536d1b5`. Go was `1.26.4 darwin/arm64`; ORAS was `1.3.0+unreleased`. The project checkout remained clean and no library file was changed. No GitHub repository was created because unique GHCR package namespaces provided the required isolation and the available token did not include repository-deletion scope.

Result: The authoritative race-enabled campaign produced 18 PASS, one registry capability NO, zero FAIL, and two N/A rows. HTTPS and classic-token authentication, independent ORAS controls, small blobs, Exists, serial Pull, progress, native PullRange, native parallel Pull, interrupted-stream resume, unreferenced retrieval, monolithic Push, 1 MiB-configured chunked Push, wrong-digest and exact-size safety, cross-repository Mount, shared-client concurrency, redirect credential isolation, and relative upload Locations passed. The 21-operation concurrency phase completed in 10.91 seconds under the race detector and independently verified four uploaded artifacts.

Compatibility limit: Empty-blob Push is NO. In two fresh-package campaigns GHCR answered HEAD for the canonical empty digest with 200 before the namespace existed, then rejected the actual zero-byte final PUT with `404 BLOB_UNKNOWN`. The library attempted cleanup and GHCR returned `405 Method Not Allowed`. GHCR also returned `405` for best-effort session deletion after wrong-digest and exact-size rejection, while neither candidate digest became available.

Protocol observations: GHCR redirected reads with `307` to off-origin storage, which served native `200`, ranged `206`, and past-end `416` responses. The configured four-worker parallel Pull made nine range requests, observed two concurrent bodies, and ended with none active. A forced break after 1 MiB resumed via native `206`. A completed unreferenced monolithic blob was readable through the library, raw HTTP, and ORAS before manifest linking in two fresh campaigns. The 8,389,341-byte chunked upload used nine PATCH requests and a final `PUT 201`. Cross-repository Mount returned `201` and exact destination bytes after independent manifest linking.

Security and cleanup: No Authorization, Cookie, Cookie2, proxy authorization, or Referer header crossed into off-origin library traffic, and no signed query value was retained. The runner deleted every unique package and polled each to 404; a separate paginated audit found zero remaining packages with the campaign prefix. The source snapshot had zero writable files, and the main worktree remained clean.

Cleanup: Recorded the final evidence and harness hashes in the results ledger, then removed the entire disposable harness, immutable source export, fixtures, and evidence directory. The external package audit remained empty and the main checkout remained clean.

Next: Commit and push only the journal results, then await the next registry candidate.

## 2026-08-12 12:46 — Docker Hub compatibility campaign

Target and isolation: Retrieved the shared `gilmanagents` Docker Hub PAT from the unlocked Bitwarden MCP item `Docker Hub PAT agents-cli-full`, without logging or writing the credential. Tested `registry-1.docker.io` from a disposable external consumer against a read-only export of merged commit `8700a0989bb82ca272ca986e2dc8eae79536d1b5`. Go was `1.26.4 darwin/arm64`; ORAS was `1.3.0+unreleased`. The main checkout remained clean and no library file was changed.

Cleanup calibration: A tiny repository create/delete probe established that the current Docker Hub API returns `202 Accepted` for asynchronous repository deletion. The cleanup gate therefore polls each repository to `404` and requires the exact prefix count to reach zero. The first probe disappeared, and every later calibration and campaign repository was likewise confirmed absent before continuing.

Harness calibration: The first write pass failed with 401 while ORAS wrote successfully using the same PAT. A raw control then proved that a directly requested `repository:...:pull,push` token opened an upload session with `POST 202`. The root cause was the disposable inherited bearer-challenge parser splitting the quoted `pull,push` value at its comma and requesting a pull-only token. A quote-aware parser plus repository and pull/push scoped token caching fixed the harness. All affected repositories were deleted before fresh reruns, and no library change was made.

Authoritative result: The final race-enabled campaign produced 19 PASS, zero NO, zero BLOCKED, zero FAIL, and two N/A rows. HTTPS and PAT authentication, independent ORAS controls, small blobs, Exists, serial Pull, progress, native PullRange, native parallel Pull, interrupted-stream resume, unreferenced retrieval, monolithic Push, empty-blob Push/Pull, 1 MiB-configured chunked Push, wrong-digest and exact-size safety, cross-repository Mount, shared-client concurrency, redirect credential isolation, and same-origin absolute upload Locations passed. The 21-operation concurrency phase completed in 11.73 seconds under the race detector and independently verified four uploaded artifacts.

Protocol observations: Docker Hub redirected reads with `307` to off-origin storage, which served native `200`, ranged `206`, and past-end `416` responses. The configured parallel Pull made nine range requests, observed three concurrent bodies in the authoritative run and four in calibration, and ended with none active. A forced break after 1 MiB resumed via native `206`. The 8,389,341-byte chunked upload used nine `PATCH 202` requests and a final `PUT 201`. A fresh empty blob completed with `PUT 201`. Cross-repository Mount returned `201`. Completed monolithic blobs were readable before manifest linking.

Reliability quirk: Wrong-digest commit correctly returned `400 DIGEST_INVALID`, and neither candidate digest became available. The library attempted upload-session deletion, but Docker Hub returned `500` on that cleanup request. Short and trailing reader cleanup returned `204`. Because the lone `500` was not followed by a successful same-request retry, the throttling/retry row remains N/A rather than being overclaimed as PASS.

Security and cleanup: No Authorization, Cookie, Cookie2, proxy authorization, or Referer header crossed into off-origin library traffic; no signed query value was retained; the PAT occurred zero times in saved evidence or harness files. Both authoritative repositories returned asynchronous delete `202`, later polled to `404`, and the broader `go-oci-blob-` prefix audit returned zero repositories. The read-only source export had zero writable files and the main worktree remained clean.

Cleanup: Recorded the evidence and harness hashes in the results ledger, then removed the disposable harness, immutable source export, fixtures, evidence, and calibration scripts. The Docker Hub prefix audit remained zero and the main checkout remained clean.

Next: Commit and push only the journal results, then await the next registry candidate.

## 2026-08-12 13:07 — GCR URL compatibility campaign

Target and service identity: Retrieved the shared GCP service-account credential from the unlocked Bitwarden secure note without logging its secret fields. Tested the `gcr.io` hostname from a disposable external consumer against a read-only export of merged commit `8700a0989bb82ca272ca986e2dc8eae79536d1b5`. Google has shut down the legacy Container Registry backend, so this campaign exercised Artifact Registry's current `gcr.io` compatibility surface. Project preflight reported `REDIRECTION_FROM_GCR_IO_ENABLED`, no `gcr.io` repository, and zero Artifact Registry repositories.

Authoritative result: Normal and `GOMAXPROCS=8` race campaigns each produced 15 PASS, two NO, three N/A, and zero FAIL rows. HTTPS/private authentication, small blobs, present/missing Exists, serial Pull, progress, three valid PullRange windows plus invalid-boundary rejection, native parallel Pull, forced interrupted-stream resume, unreferenced retrieval, monolithic Push, wrong-digest and exact-size safety, cross-repository Mount, shared-client mixed concurrency, and relative upload Locations passed. The race detector reported no race. Independent ORAS retrieval matched all 3,146,061 bytes and digest `sha256:3c219121fac4cc317d0b4046539d879a724b17b492b430589f30033151419b79`.

Compatibility limits: Empty-blob Push is NO: the registry opened a session but rejected the zero-byte final PUT with `400 Bad Request`. Chunked Push is NO at the 1 MiB configuration: the first PATCH returned `202`, the next request returned `405 Method Not Allowed`, and the digest remained absent. Range-ignored fallback, off-origin credential scope, and hosted throttling retry remained N/A because the live service honored ranges, kept responses on `gcr.io`, and did not throttle.

Concurrency and safety proof: Seventeen ranged responses produced exact ordered bytes, three response bodies overlapped, and zero remained active. A forced body break resumed through three ranged requests. Twelve barrier-started Pull, PullRange, Exists, Push, and Mount operations completed on shared clients; every pushed and mounted result passed an independent exact-byte GET. Wrong-digest rejection left both possible digests absent and cleanup returned `204`. Both short and trailing reader failures left the digest absent and cleanup returned `204`.

Cleanup: The first push auto-created the predefined `projects/agents-shared-505304/locations/us/repositories/gcr.io` repository. It contained no packages or Docker images because the tests published no manifests. The cleanup gate matched the exact resource and creation time, deleted it through the Artifact Registry long-running operation, confirmed the repository returned `404`, and confirmed the project repository count returned to zero. The pre-existing redirection setting remains enabled. No library file was modified.

Next: Commit and push only the journal results, then await the next registry candidate.

## 2026-08-12 13:48 — Quay.io compatibility campaign

Target and isolation: Retrieved the shared `gilmanagents` Quay account credential from the unlocked Bitwarden vault. Because the account is backed by Red Hat SSO and has no standalone Quay password, created a disposable robot and granted it write access only to two uniquely named public repositories. Tested `quay.io` from a disposable external consumer against a read-only export of merged commit `8700a0989bb82ca272ca986e2dc8eae79536d1b5`; the export retained zero user-writable files and no library source was changed.

Authoritative result: Final normal and race campaigns each produced 17 PASS, one NO, and two N/A rows. HTTPS robot authentication, small blobs, present/missing Exists, serial Pull, progress, PullRange, native parallel Pull, interrupted-stream resume, unreferenced retrieval, monolithic Push, 1 MiB-configured chunked Push, wrong-digest and exact-size safety, cross-repository Mount, shared-client mixed concurrency, off-origin credential isolation, and absolute upload Locations passed. The race detector reported no race.

Compatibility limit: Empty-blob Push is NO. Quay returned success from the zero-byte Push and then answered HEAD as present, but both raw GET and library Pull returned `404`. An independent ORAS blob fetch also returned not found. This reproduces across the final normal and race campaigns and contrasts with the independently fetched and SHA-256-verified 2,097,289-byte control blob.

Protocol observations: Nine native ranged `206` responses returned exact parallel bytes, with two response bodies overlapping and none remaining active. A forced break after 128 KiB resumed through ranged requests. A 3,146,061-byte chunked upload used four PATCH requests and returned exact bytes. Cross-repository Mount succeeded and the destination returned exact bytes. Forty-three off-origin requests carried no registry authorization, and successful uploads followed same-origin absolute Locations.

Cleanup: Deleted both uniquely named public repositories and verified the account repository list returned to zero. Deleted the disposable robot and verified the account reported no robot accounts. The main worktree remained clean. Recorded the authoritative results and hashes in the dedicated registry results ledger, then removed the disposable credentials, harness, source export, and evidence.

Next: Commit and push only the journal results, then await the next registry candidate.

## 2026-08-12 15:06 — Azure Container Registry compatibility campaign

Target and isolation: Used the user's temporary Microsoft device login against the `Pay-As-You-Go` subscription, then switched data-plane work to a temporary admin credential scoped to one disposable Basic ACR. Preflight found the unrelated `Lab` resource group, zero existing ACR instances, and the `Microsoft.ContainerRegistry` provider unregistered. Registered the provider temporarily, created a uniquely tagged resource group containing exactly one registry in `westus2`, and left `Lab` untouched. Tested from an external consumer against a read-only export of commit `8700a0989bb82ca272ca986e2dc8eae79536d1b5`; no library source changed.

Authoritative result: Final normal and race campaigns each produced 18 PASS and two N/A rows. HTTPS/admin-bearer authentication, small blobs, present/missing Exists, serial Pull, progress, PullRange, native parallel Pull, interrupted-stream resume, unreferenced retrieval, monolithic Push, empty-blob Push/Pull, 1 MiB-configured chunked Push, wrong-digest and exact-size safety, cross-repository Mount, shared-client mixed concurrency, off-origin credential isolation, and mixed absolute/relative upload Locations passed. The race detector reported no race.

Protocol observations: Nine native ranged `206` responses returned exact parallel bytes with three response bodies overlapping and none remaining active. A forced break after 128 KiB resumed through ranges. A 3,146,061-byte chunked upload used four PATCH requests and returned exact bytes. Cross-repository Mount succeeded and the destination returned exact bytes. Sixty-two off-origin requests carried no registry authorization. ACR was the second tested hosted registry after Docker Hub to correctly serve the canonical empty blob; ORAS independently fetched both the exact 2,097,289-byte control and exact zero-byte blob.

Cleanup: Confirmed the disposable resource group contained exactly the tagged ACR, deleted the whole group, and verified the subscription returned to zero ACR instances. Restored `Microsoft.ContainerRegistry` to its original unregistered state. Removed the scoped registry admin credential, harness, source export, and evidence. The unrelated `Lab` group and the clean main checkout were untouched.

Next: Commit and push only the journal results, then await the next registry candidate.

## 2026-08-12 16:26 — Harbor compatibility campaign

Target and isolation: Deployed the official Harbor `v2.15.2` online-installer stack locally with a campaign-only CA, filesystem storage, and private source and destination projects. Harbor's `linux/amd64` service images ran under Docker emulation on the `linux/arm64` host. The consumer used a read-only export of commit `8700a0989bb82ca272ca986e2dc8eae79536d1b5`; no library source changed.

Authoritative result: Fresh normal and race repository paths each produced 17 PASS and three N/A rows with identical matrices. HTTPS/Bearer authentication, small blobs, present/missing Exists, serial Pull, progress, PullRange, native parallel Pull, interrupted-stream resume, unreferenced retrieval, monolithic Push, empty-blob Push/Pull, 1 MiB-configured chunked Push, wrong-digest and exact-size safety, cross-repository Mount, shared-client concurrency, and absolute upload Locations passed. Range-ignored fallback, off-origin credential scope, and throttling remained N/A because local Harbor served native ranges, used filesystem storage without redirects, and did not emit `429`.

Protocol and concurrency proof: ORAS independently seeded a 4 MiB source layer. Parallel Pull used sixteen `206` requests with all four configured response bodies active and none remaining afterward. A forced body break resumed through a range request. The 3,145,839-byte chunked upload used four acknowledged PATCHes and an empty final `PUT 201`. Wrong-digest rejection left both possible digests absent and attempted cleanup. Mount returned `201`; raw HTTP and ORAS independently verified the destination. Twenty barrier-started mixed operations created eight artifacts, all independently fetched exactly; the race detector reported no race.

Cleanup: Stopped and removed the exact nine-container Harbor Compose deployment and its network, then removed the private projects, filesystem data, TLS material, credential, immutable export, harness, fixtures, logs, and evidence. Confirmed port 9443 was closed and no Harbor campaign container or network remained. The unrelated GitHub MCP container stayed running, and the main checkout remained clean.

Next: Commit and push only the Harbor journal results, then continue with the next local OSS registry candidate.

## 2026-08-12 16:50 — GitLab native registry compatibility campaign

Target and isolation: Deployed the official GitLab Community Edition `19.2.1` native arm64 Docker image with its bundled container registry, distinct campaign-TLS GitLab and registry endpoints, filesystem storage, and private source and destination projects. GitLab's real `/jwt/auth` flow issued repository-scoped registry tokens from a disposable personal access token. The consumer used a read-only export of commit `8700a0989bb82ca272ca986e2dc8eae79536d1b5`; no library source changed.

Authoritative result: Fresh normal and `GOMAXPROCS=8` race repository paths each produced 17 PASS and three N/A rows with identical matrices. HTTPS/GitLab Bearer authentication, small blobs, present/missing Exists, serial Pull, progress, PullRange, native parallel Pull, interrupted-stream resume, unreferenced retrieval, monolithic Push, empty-blob Push/Pull, 1 MiB-configured chunked Push, wrong-digest and exact-size safety, cross-project Mount, shared-client concurrency, and absolute upload Locations passed. Range-ignored fallback, off-origin credential scope, and throttling remained N/A because the bundled filesystem registry served native ranges, did not redirect storage, and emitted no `429`.

Protocol and concurrency proof: ORAS independently seeded a 4 MiB OCI layer. Parallel Pull used sixteen `206` requests with all four configured response bodies active and none remaining afterward. A forced body break resumed through a range request. The 3,145,839-byte chunked upload used four acknowledged PATCHes and an empty final `PUT 201`. Wrong-digest rejection left both possible digests absent and attempted cleanup. Mount returned `201`; raw HTTP and ORAS independently verified both fresh destinations. Twenty barrier-started mixed operations created eight artifacts, all independently fetched exactly; the race detector reported no race.

Cleanup: Deleted all 21 GitLab registry repositories, polled both project registry lists to empty, permanently deleted both projects and confirmed `404`, and revoked the personal access token. Removed the container, network, native arm64 image, TLS, root password, GitLab data and logs, immutable export, fixtures, harness, and evidence. Ports 9444 and 5055 were closed, the unrelated GitHub MCP container remained running, and the main checkout stayed clean.

Next: Commit and push only the GitLab journal results, then continue with the next OSS registry candidate.

## 2026-08-12 17:10 — Nexus Repository OSS compatibility campaign

Target and product boundary: Current Nexus `3.93.1` started successfully but blocked all registry traffic until the Community Edition EULA was accepted. Because the requested target was OSS, accepted no EULA, wrote no test data, reset the disposable volume, and used native-arm64 Nexus Repository OSS `3.76.0` from the final pre-Community release line. Deployed two hosted Docker repositories behind one campaign-TLS registry host so cross-repository Mount began from independently proven destination absence. The consumer used a read-only export of commit `8700a0989bb82ca272ca986e2dc8eae79536d1b5`; no library source changed.

Authoritative result: Five fresh salted campaigns produced a conservative final matrix of 14 PASS results including observed unreferenced retrieval, three NO, and three N/A. HTTPS/Nexus Bearer authentication, small blobs, present/missing Exists, serial Pull, progress, PullRange, native parallel Pull, interrupted-stream resume, unreferenced retrieval, monolithic Push, empty-blob Push/Pull, 1 MiB-configured chunked Push, shared-client concurrency, and mixed upload Locations passed. Native ranges left fallback N/A; filesystem storage left off-origin scope N/A; no 429 left throttling N/A. Both race campaigns completed with no race report.

Compatibility limits: Wrong-digest rejection is NO. Nexus returned `PUT 400`, yet exposed the exact uploaded bytes at the claimed digest even though their SHA-256 differed; the library's verified Pull correctly returned `ErrDigestMismatch`. Exact-size rejection is NO because the timing-sensitive trailing-byte case returned an error but still committed the exact declared prefix in three of five fresh campaigns, including both race runs; the short-reader case always remained absent. Cross-repository Mount is NO: the distinct destination repository was absent, Nexus returned `202`, the library safely deleted the opened session and returned `(false, nil)`, and the destination remained absent.

Protocol and concurrency proof: ORAS independently seeded and fetched an 8,388,865-byte OCI layer. Parallel Pull used nine `206` requests and reached all four configured open response bodies with zero remaining afterward. The 8,389,341-byte chunked upload used nine PATCHes and a final `PUT 201`. Four Pushes, three Pulls, three PullRanges, two Exists checks, and two Mount declines shared one client; all four created artifacts passed independent exact-byte GETs.

Cleanup: Deleted both hosted repositories through the Nexus API and confirmed the exact names absent. Removed the Compose containers, network, filesystem volume, both Nexus image versions, proxy image, TLS, credential, read-only export, fixtures, harness, logs, and evidence from the active filesystem. Ports 9446 and 5057 were closed; only the unrelated GitHub MCP container remained running. The main checkout stayed clean. Recorded the matrix and evidence hashes in the dedicated registry results ledger.

Next: Commit and push only the Nexus journal results; the requested registry campaign series is complete.

## 2026-08-12 19:12 — Durable registry compatibility harness

Decision: Retained the registry-neutral compatibility evidence engine, not the disposable provider automation. The new nested consumer module lives under `test/` with a module path outside `github.com/imgoci/go-oci-blob`, so Go enforces the same `internal/` boundary as a real consumer. It owns strict config, maintained ORAS authentication, lossy wire observation, 20 public-API probes, independent byte/digest controls, conservative normal/race aggregation, secret-safe reports, and focused unit/race tests. Provider provisioning, secret retrieval, cloud CLIs, temporary TLS/Compose, and cleanup remain in the agent-operated how-to runbook; no provider SDK or Compose asset is committed.

Evidence policy: Infrastructure and ambiguous transport failures invalidate a run instead of becoming `PASS` or `NO`. Unsupported results require fresh reproduction; unsafe persistence requires independently fetched exact candidate bytes. Reports record unique seed identities and reject reused runs, repositories, or digests. Mount can be operator-attested `BLOCKED` only for an externally verified policy. Race reports query the runtime race count and the runbook uses `GORACE=halt_on_error=1`. Wire evidence keeps only method, endpoint category, status, range metadata, origin/route, Location form/query names, and header-presence booleans; upload capabilities and query values are never retained.

Repository fit: Registered the nested module as Moon project `compat`; root `check` now includes its format, lint, build, unit, and race tasks but never a live registry campaign. Root `go list ./...` excludes the nested module. Pinned ORAS `1.3.3` in mise for the independent control. Added the Diataxis how-to `docs/docs/how-to/refresh-registry-compatibility.md`, including the nine-registry lifecycle contracts and cleanup invariants.

Calibration: A disposable authenticated TLS Distribution `registry:2.8.3` campaign used a read-only library export, fresh random 3,146,509-byte seeds, fresh normal/race repositories, ORAS/raw seed controls, and mode-0600 sensitive files. Both runs were valid; the race run was race-clean; aggregation produced 17 `PASS`, three `N/A` (range-ignored fallback, off-origin redirect scope, natural throttling), and no `NO` or `BLOCKED`. Initial evidence hashes before the final source-body/transport distinction were normal `b0cd84732dee91eda0996abc253ec100bfd68323de090a920f8b750e3fdd2962`, race `23ab7d61f82e74457fcd7bc2e1b1c14b0624f519219e63a6bb2d142d42551120`, aggregate `7a847a12a117e34a370470ee9e5912a1a46a97b1586ed261621a3f0507fc8954`; a final fresh calibration is in progress for the exact branch snapshot.

Verification: `root:check` passed all 14 Moon tasks, including tagged Registry/Zot E2E, strict docs, root build/test/lint, and nested consumer format/lint/build/unit/race. Nested `go mod tidy -diff`, `go mod verify`, `go vet`, uncached normal tests, and uncached race tests passed. Two independent final reviews found no remaining P0/P1 issue after the observer began distinguishing causal source-body errors from unrelated transport failures.

Next: Finish exact-snapshot calibration, commit and publish `feat/registry-compat-harness`, open the GitHub PR, and await review. Keep session 005 open unless explicitly asked to close it.

## 2026-08-12 19:18 — Final harness snapshot calibrated

The final request-body-aware wire observer snapshot passed the full disposable TLS/authenticated Distribution `registry:2.8.3` campaign with pinned ORAS `1.3.3`. Fresh normal and race runs were both valid, the race run was clean, and the two-run aggregate again produced 17 `PASS`, three `N/A` (range fallback, off-origin scope, natural throttling), zero `NO`, and zero `BLOCKED`. Final evidence SHA-256 values: normal `7adf2c42ee5db66422e3ae48c0e6bd6b1ce07a6217613df8f9b0f5fbf258be20`; race `f272a73ff13380674ef98fc2c8955f2a47d478104e201bd8788364934f8abeb7`; aggregate `12274b871ecec1c066e2986cc48edfb8a75a8670eea16adcbdf543a602f51c7d`.

Cleanup proof: mode-0600, actual credential and Basic-auth sentinel, read-only export, named-container removal, and temporary-directory audits passed. No compatibility container, `/tmp` campaign folder, generated binary, credential, fixture, or raw evidence remains in the active filesystem.

Next: Commit and publish the feature branch and open its GitHub PR. Keep session 005 open unless explicitly asked to close it.

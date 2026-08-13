# Refresh the registry compatibility matrix

Use the compatibility harness in `test/` to repeat the library's live-registry
test corpus. The harness checks transfer behavior. The operator or agent
provisions registries, supplies credentials, establishes independent controls,
and removes everything created for the campaign.

This workflow assumes an agent that can inspect current provider documentation,
write disposable scripts, and adapt commands to current service APIs. Keep those
scripts, credentials, TLS material, and generated Compose files outside the
repository. Do not turn them into permanent provider adapters.

The tested corpus is:

- Amazon ECR Private
- GitHub Container Registry (GHCR)
- Docker Hub
- the `gcr.io` URL served by Artifact Registry
- Quay.io
- Azure Container Registry (ACR)
- Harbor
- GitLab's native container registry
- Sonatype Nexus Repository OSS 3.76.0

## Before you start

Use an account where creating and deleting disposable repositories is allowed.
Run `mise install` to install the pinned Go and ORAS versions. Install the
provider CLI needed for the selected registry. Local campaigns also require
Docker. Run harness and ORAS commands through `mise exec --` unless mise is
already activated in the shell.

Choose a campaign ID that cannot collide with another run. Include it in every
repository, project, resource group, container, network, volume, TLS filename,
and temporary directory created by the campaign.

Record these baselines before changing anything:

- the exact library commit and `git status`;
- existing resources that share the intended name prefix;
- relevant account-wide settings;
- local containers, images, networks, volumes, and listening ports;
- the registry product, version, storage backend, and platform.

Stop if a destructive cleanup target cannot be distinguished from a pre-existing
resource. Do not broaden a prefix or infer ownership from a familiar name.

## Preserve the campaign invariants

These conditions distinguish compatibility evidence from a broken harness run:

1. Test a clean, exact commit from a read-only export. Do not modify library code
   for the campaign.
2. Use a fresh run ID and fresh payloads for every normal, race, and reproduction
   run. A payload digest must not carry over between runs.
3. Use distinct source and destination repositories. Immediately before Mount,
   prove the destination digest absent with authenticated `HEAD` and `GET`.
4. Seed the source control with ORAS, then verify it independently with ORAS and
   raw HTTP before using it to judge library reads.
5. Run both a normal executable and a race-enabled executable against fresh
   repositories and payloads.
6. Treat TLS, authentication, provisioning, timeout, quota, control, and harness
   failures as invalid runs. Never translate them into `NO`.
7. Require independent raw or ORAS evidence for every published `PASS` or `NO`
   that concerns stored bytes.
8. Reproduce an unsupported result with a fresh payload before publishing `NO`.
   For unsafe persistence, also fetch and hash the stored bytes.
9. Record the current corpus result ledger separately before comparing it with
   the new aggregate. Treat prior results as regression context, not expected
   answers.
10. Do not publish a campaign until resource cleanup, secret scanning, and the
    final clean-checkout check all pass.

The destination-absence requirement is especially important for Mount. Some
products expose a blob across namespaces backed by the same internal store. Use
separate provider repositories or connectors when necessary. An already visible
blob cannot prove that Mount copied or linked anything.

## Create an immutable consumer export

Export the exact revision to a new temporary directory. Run the nested `test/`
module from that export so the harness consumes the public library at the
recorded revision.

```sh
repo_root=$(git rev-parse --show-toplevel)
library_commit=$(git rev-parse HEAD)
campaign_tmp=$(mktemp -d "${TMPDIR:-/tmp}/go-oci-blob-compat.XXXXXX")

git -C "$repo_root" archive "$library_commit" | tar -x -C "$campaign_tmp"
chmod -R a-w "$campaign_tmp"
cd "$campaign_tmp/test"
export GOFLAGS=-mod=readonly
```

Do not put generated files inside the read-only export. Point the campaign
configuration's `run.work_dir` and all output paths at a separate mode-`0700`
temporary directory.

## Provision one isolated target

Provision the target by following its contract below. Use current official
provider instructions and disposable scripts as needed. Do not commit generated
Compose, proxy, certificate, or provider configuration.

For each normal or race run:

1. Create fresh source and destination repository names.
2. Create a new salted seed file large enough to span several parallel chunks.
3. Seed the source with ORAS. A manifest may reference the control blob when the
   registry requires it; the unreferenced-blob probe uses a separate payload.
4. Fetch the seed through ORAS and authenticated raw HTTP. Require exact size and
   SHA-256 agreement.
5. Prove the destination test digest absent.
6. Write a mode-`0600` JSON configuration outside the source export.

Create the ORAS registry configuration with `mise exec -- oras login` and feed
the password through standard input. Seed the control blob with
`mise exec -- oras blob push`, then publish a small OCI manifest that references
it when the registry only exposes image-referenced layers. Keep the exact seed
file and its SHA-256 digest for the harness input. Provider syntax varies, so
inspect the pinned ORAS help and the current registry policy rather than copying
a credential-bearing one-liner into the runbook.

The configuration has this shape:

```json
{
  "schema_version": 1,
  "registry": {
    "id": "registry-id",
    "product": "Product name",
    "version": "service or product version",
    "backend": "hosted or storage backend",
    "host": "registry.example.com"
  },
  "run": {
    "id": "unique-run-id",
    "library_commit": "full-lowercase-commit",
    "source_repository": "source/name",
    "destination_repository": "destination/name",
    "work_dir": "/private/path/to/disposable-work",
    "blocked_features": []
  },
  "tls": {"ca_file": "/private/path/to/ca.pem"},
  "auth": {
    "username": "username",
    "password_file": "/private/path/to/password",
    "require_anonymous_denial": true
  },
  "oras": {
    "binary": "oras",
    "registry_config_file": "/private/path/to/oras-auth.json"
  },
  "seed": {
    "file": "/private/path/to/seed.bin",
    "digest": "sha256:..."
  },
  "parameters": {
    "parallel_workers": 4,
    "parallel_chunk_bytes": 262144,
    "upload_chunk_bytes": 1048576,
    "operation_timeout": "90s",
    "campaign_timeout": "12m",
    "absence_settle_time": "1s"
  }
}
```

Use exactly one of `password_file`, `refresh_token_file`, or
`access_token_file`. The ORAS registry configuration is an independent verifier
input, not a source of credentials for the library transport. Increase the
absence settle time when a hosted registry has observable commit lag.

Leave `blocked_features` empty unless an independently verified external policy
prevents a probe. The only accepted value is `cross_repository_mount`. Record
the policy evidence outside the report and use the same value in every run that
will be aggregated. A registry decline without that attestation is `NO`, not
`BLOCKED`.

## Run the normal and race campaigns

Run the normal campaign from the exported `test/` module:

```sh
mise exec -- go run ./cmd/registry-compat run \
  --config "$normal_cfg" \
  --output "$normal_report" \
  --immutable-consumer
```

Build one race-enabled executable and run it with immediate termination on a
race, against a fresh configuration, repositories, seed, and run ID:

```sh
mise exec -- go build -race -o "$campaign_work/registry-compat-race" ./cmd/registry-compat

GORACE=halt_on_error=1 mise exec -- "$campaign_work/registry-compat-race" run \
  --config "$race_cfg" \
  --output "$race_report" \
  --immutable-consumer
```

Do not reuse repositories between these runs. A normal run can change registry
state in ways that hide a race-run defect.

Aggregate the valid reports:

```sh
mise exec -- go run ./cmd/registry-compat aggregate \
  --output "$aggregate" \
  "$normal_report" "$race_report"
```

Pass additional fresh reports after the race report when reproducing an
inconsistent or unsupported result.

## Apply the feature proof contract

The harness emits the same 20 rows for every registry. Use these minimum proof
requirements when reviewing its structured assertions and independent controls.

| Feature | Required evidence |
|---|---|
| HTTPS and authentication | A trusted TLS hostname; the protected anonymous request is denied when required; authenticated `/v2/` access succeeds. |
| Small blob, about 1 KiB | A fresh approximately 1,027-byte payload survives library transfer and exact independent retrieval. |
| `Exists`, present and missing | The seeded digest returns true and a fresh synthetic digest returns false, with the corresponding authenticated `HEAD` outcomes. |
| Serial `Pull` | The ORAS-seeded body reaches verified EOF with exact bytes and SHA-256. |
| Progress reporting | Counts are monotonic, reach the transferred size, use the exact total or documented `-1` when unknown, and do not overlap within one transfer. |
| `PullRange` | Beginning, middle, and tail windows are exact; each `206` has a matching `Content-Range`; invalid past-end cases are rejected. |
| Parallel `Pull` | Multiple ranged requests return exact ordered bytes, at least two response bodies overlap, and no body remains active at completion. |
| Parallel range-ignored fallback | A registry naturally ignores a range and the client returns an exact single stream. Use `N/A` when the registry serves ranges. Do not force the result with a mock. |
| Interrupted `Pull` resume | A consumer-side body failure is injected after delivered bytes; a ranged continuation starts at the delivered offset and the final body is exact. |
| Unreferenced blob retrieval | Before manifest linking, library Pull, raw GET, and ORAS blob fetch agree on the completed payload. Report the result as observed behavior. |
| Monolithic `Push` | The default path uses an upload POST, no PATCH, one body-bearing final PUT, terminal `201`, and exact independent retrieval. |
| Empty blob `Push` and `Pull` | Perform a real zero-byte upload even if the canonical digest appears globally present; require an exact zero-byte retrieval. |
| Chunked `Push` | More than one acknowledged PATCH is used when the fixture permits it; acknowledged ranges advance correctly; the final PUT commits exact bytes. |
| Wrong-digest rejection | Push returns an error and both claimed and calculated digests stay absent through the settle window. Visible mismatched content is `NO`, even if Push returned an error. |
| Exact-size rejection | Both short and trailing readers return errors. Every possible digest stays absent through the settle window. Persistence of the declared prefix is `NO`. |
| Cross-repository `Mount` | The destination is absent first. A successful mount returns `201`, performs no PATCH or PUT, and yields exact destination bytes. A safe `202` decline with destination absence is `NO`. |
| Shared-client concurrency | Barrier-started mixed operations share one client; all created artifacts verify independently; response bodies close; the race detector stays clean. A valid Mount decline is allowed when Mount is `NO`. |
| Off-origin redirect credential scope | When off-origin traffic occurs, no registry Authorization, Proxy-Authorization, Cookie, Cookie2, or Referer crosses origins. Use `N/A` when no off-origin request occurs. |
| Upload `Location` handling | The client follows returned relative or absolute opaque locations without changing provider state. Evidence records only the form, never opaque query values. |
| Retry after registry throttling | A real registry `429` or `5xx` is followed by a successful retry within the configured budget. Do not load-test a hosted service to manufacture throttling; otherwise use `N/A`. |

For wrong-digest and exact-size tests, immediate absence is insufficient. Poll
through the configured settle window. Nexus OSS demonstrated that a registry can
return an error and still make bad or partially consumed input visible.

## Aggregate conservatively

The report labels mean:

| Label | Meaning |
|---|---|
| `PASS` | The behavior ran and its result was independently verified. |
| `NO` | The registry and library combination did not support the behavior. |
| `BLOCKED` | Registry policy, account permission, or environment state prevented a valid measurement. |
| `N/A` | The live registry did not exercise an opportunistic path. |

Apply these rules before updating documentation:

- Do not aggregate any report with `run_valid: false` or a failed independent
  control.
- Require a race-enabled report with `race_clean: true`.
- Publish `PASS` only when every valid run that exercised the feature passed.
- Publish `N/A` only when no valid run exercised the path. `PASS` plus `N/A`
  aggregates to `PASS` when the exercised run passed.
- Publish an ordinary unsupported result as `NO` only after two fresh runs
  reproduce it.
- Publish unsafe persistence as `NO` after a fresh reproduction and independent
  byte and digest confirmation. Record its frequency when timing varies.
- A confirmed `NO` takes precedence over a PASS. Intermittent corruption or
  persistence is still incompatibility.
- Use `BLOCKED` only for an external prerequisite that remains unavailable after
  setup remediation. Harness bugs, expired credentials, invalid controls, and
  timeouts invalidate the run instead.
- Do not publish unexplained disagreement. Repeat fresh campaigns, up to five
  when necessary, and record the observed frequency.

Keep the normal report, race report, aggregate JSON, and SHA-256 identities of
any disposable raw evidence long enough to review and update the compatibility
documentation. Do not commit raw request logs or temporary campaign files.

## Protect credentials and opaque registry state

- Put secrets only in mode-`0600` files under the mode-`0700` campaign
  directory. Do not put them in command arguments, environment dumps,
  checked-in configuration, or evidence prose.
- Feed CLI passwords through standard input. Disable shell tracing before
  handling a credential.
- Never retain Authorization headers, cookies, token responses, request bodies,
  private keys, Docker auth files, or upload-session URLs.
- Strip query strings and fragments from recorded `Location` values. Signed and
  opaque query bytes can be credentials.
- Scan temporary source, scripts, logs, reports, and evidence for the exact
  secret values and their Basic-auth encodings. The scan must print only a
  count or filenames, never the matching line or secret.
- Revoke disposable PATs and robots. Delete registry login files, token caches,
  campaign CA private keys, and credential files after cleanup verification.

An independent control separates auth defects from registry incompatibility.
For example, a parser that splits a quoted Bearer scope at its comma can obtain
a pull-only token and make every Push return `401`, while ORAS writes still
succeed. That is an invalid harness run.

## Follow the registry-specific contract

Use these requirements with current official provider instructions. They define
the topology, isolation, and cleanup proof; they do not prescribe durable
automation.

### Amazon ECR Private

- Use two disposable repositories in one region with matching encryption.
- Read the account's `BLOB_MOUNTING` setting before testing. Mount requires it
  enabled. Do not change an account-wide setting without explicit approval, and
  record whether it must be restored.
- Obtain short-lived credentials through the configured `aws-vault` profile.
- Delete both repositories and require the exact campaign-prefix count to return
  to its baseline.

### GitHub Container Registry

- Use two disposable package namespaces. Do not create a GitHub repository when
  package namespaces are sufficient.
- Obtain a token with the required package scopes from the configured GitHub
  credentials without copying it into evidence.
- Delete both packages, poll each package API to `404`, and audit the paginated
  package list for the exact campaign prefix.

### Docker Hub

- Create two disposable repositories under the shared test account. Retrieve
  the account credential from the configured secret manager.
- Parse quoted Bearer challenge values correctly, including comma-containing
  scope values.
- Repository deletion is asynchronous. Poll both repositories to `404`, then
  require the account's exact campaign-prefix count to return to its baseline.

### `gcr.io`, Artifact Registry-backed

- Record both the `gcr.io` hostname and the serving Artifact Registry backend.
  The legacy Container Registry backend is retired.
- Read the project's redirection state and repository inventory before the
  first push.
- Delete the backend `gcr.io` repository only if the campaign created and
  positively identified it. Never delete a pre-existing shared repository.
- Wait for the deletion operation, require readback absence, and preserve the
  original redirection state.

### Quay.io

- Create two uniquely named repositories and a disposable robot whose access is
  limited to those repositories.
- Record repository visibility because it can affect independent controls.
- Delete both repositories and the robot. Require repository and robot counts
  for the campaign to return to baseline.

### Azure Container Registry

- After the user completes Azure sign-in, create one uniquely tagged resource
  group containing one disposable Basic registry.
- Record the `Microsoft.ContainerRegistry` provider registration state before
  provisioning. If the campaign changes it, restore that state afterward.
- Use a temporary credential scoped to the disposable registry.
- Delete the whole resource group, require zero campaign registries, and confirm
  unrelated resource groups remain unchanged.

### Harbor

- Use the current official installer to generate a disposable Compose stack.
  Use filesystem storage, two private projects, and campaign-only TLS.
- Record image versions and platform emulation when the host architecture does
  not have a native image.
- Delete the projects, stop the exact generated stack, and remove its data,
  network, volume, TLS, and images that were absent from the baseline.

### GitLab native container registry

- Use the current official Community Edition image with its bundled registry,
  filesystem storage, two private projects, and distinct campaign TLS endpoints
  for GitLab and the registry.
- Create a disposable token with only the API and registry access needed by the
  campaign.
- Delete registry repositories before deleting the projects. Confirm both
  projects return `404`, revoke the token, and remove the exact local deployment
  and data.

### Sonatype Nexus Repository OSS

- Test `3.76.0` for the existing OSS matrix column. It is the final release line
  before Sonatype's Community Edition transition. Testing current Community
  Edition is a separate product/version result.
- Do not accept a new EULA as part of an OSS refresh.
- Create two hosted Docker repositories with distinct connectors, filesystem
  storage, the real Docker Bearer Token Realm, and a campaign TLS reverse proxy.
- Delete both hosted repositories through the Nexus API and confirm absence
  before removing the exact local stack, volume, proxy, TLS, and campaign-only
  images.

## Complete cleanup

Cleanup is part of the test. Run it even when a probe or control fails.

1. Delete only resources positively identified by the campaign ID and the
   recorded preflight inventory.
2. Poll asynchronous provider deletion until the resource is absent. Require
   exact prefix counts to return to baseline.
3. Revoke disposable tokens and robots. Restore every account-wide or
   subscription-wide setting changed by the campaign unless the owner explicitly
   requested the new state to remain.
4. For local deployments, remove the exact containers, network, volumes, data,
   proxy, TLS, and campaign-only images. Confirm all campaign ports are closed.
5. Perform the secret scan, then remove credential files, auth configuration,
   private keys, generated scripts, fixtures, binaries, and raw logs.
6. Hash retained redacted reports and aggregates. Remove the remaining temporary
   directory when those identities have been recorded.
7. Confirm the original checkout still has the recorded commit and clean status.
8. Compare final provider and Docker inventories with the baseline. Investigate
   any difference before declaring the campaign complete.

Update the published matrix only from the reviewed aggregate and concise,
redacted observations. Record the tested service or product version and backend;
a result without that identity is not reproducible compatibility evidence.

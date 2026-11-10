# Docker releases and Komodo

Paperlens is distributed as `ghcr.io/guigui42/paperlens` for **Linux amd64 and
arm64**. The image includes Go's application binary and the checksum-pinned
**Copilot CLI 1.0.109**, paired with Go SDK **1.0.31**. No Node installation,
Copilot CLI installation, frontend build or source checkout is required on the
Docker host.

## Install a release

1. Download `compose.release.yaml`, `paperlens.env.example`,
   `image-reference.txt` and `checksums.txt` from the chosen
   [GitHub release](https://github.com/guigui42/paperlens/releases).
2. Verify downloaded assets against `checksums.txt`. Download all listed assets
   if using `sha256sum -c checksums.txt` without selecting individual lines.
3. Copy `paperlens.env.example` to `.env`. The release asset is pinned to the
   exact multiarchitecture image digest. Set your Paperless origin (without
   `/api/`), canonical Paperlens browser origin and the **Docker host's** secret
   file paths. Confirm the selected model is enabled for your identity.
4. Create the two credential files outside the source/deployment repository,
   with directory mode `0700`, file mode `0600`, and ownership allowing container
   UID/GID `10001` to read them. Prefer a dedicated least-privilege Paperless
   identity. Supply an entitled Copilot credential of a supported token type.
   Never paste credentials into Compose, build arguments or Git.
5. Pull and start the image:

```sh
docker compose -f compose.release.yaml pull
docker compose -f compose.release.yaml up -d
docker compose -f compose.release.yaml ps
docker compose -f compose.release.yaml exec paperlens paperlens health
```

`PAPERLENS_BIND_IP=127.0.0.1` is the safe default. There is **no application
login**. Bind a specific LAN/VPN address only after establishing network access
controls, or use an authenticated reverse proxy. Set `PAPERLENS_ORIGIN` to the
exact browser origin and preserve its Host through the proxy.

### Registry access

[GHCR makes newly published packages private by default](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry#pushing-container-images),
even when the source repository is public. Do not assume anonymous pulls work.
The owner can explicitly choose public visibility on the package settings page,
or configure read access on each Docker execution host. This release workflow
does not change package visibility or add long-lived GitHub secrets.

For private images, use an appropriate **registry** credential with
`read:packages` and `docker login ghcr.io --password-stdin`, or Komodo's registry
credential settings. Registry credentials are separate from Copilot credentials:
GHCR's PAT requirements are not the Copilot SDK's supported-token requirements.
Never reuse the app's inference token as a guessed registry credential.

## Later inclusion in Komodo

Use the release Compose file as a Komodo Stack. It has an `image:` reference and
no `build:` section, so Komodo does not need the Go toolchain or Copilot CLI.

- Set `PAPERLENS_IMAGE` from the release's `image-reference.txt`. A version tag is
  convenient, but a digest records exactly what was deployed.
- Configure the remaining example environment variables through the Stack's
  environment configuration. They are non-secret values and secret **paths**.
- Place credentials on the Docker execution host, not merely on the Komodo web
  server. File-backed Compose secrets preserve their host permissions.
- Keep the `paperlens_data` volume for browser keys and upload receipts. Keep a
  stable Stack/Compose project name across updates so the same volume is reused.
- Keep both tmpfs mounts: `/runtime` needs `exec` for the CLI's native addon;
  `/tmp` is `noexec` and holds temporary uploads. Both mounts are RAM-backed.
- Keep UID/GID 10001, the read-only root, dropped capabilities, health check and
  shutdown grace period. Do not mount your normal Copilot home or Docker socket.
- If GHCR is private, configure registry pull access on the execution host.

No Komodo resource or household deployment is created by the release workflow.
This file is preparation for a later deployment.

## Updates and rollback

Change only `PAPERLENS_IMAGE` to the new release digest, pull, then redeploy the
Stack. Preserve the data volume and credentials. To roll back, restore the prior
image digest and redeploy. Do not delete the data volume as an update step.

The default release environment uses `gpt-6.1-sol`; model availability is checked
for the configured server identity before inference. Uploads remain disabled
until `PAPERLENS_UPLOAD_TYPES` is explicitly set after verifying parser support.

## Build and publish

The first published release is
[v0.1.1](https://github.com/guigui42/paperlens/releases/tag/v0.1.1).
That immutable release contains CLI 1.0.105 and SDK 1.0.28. New runtime versions
ship in a new patch release; old release tags and digests are never rewritten.
[Its Actions run](https://github.com/guigui42/paperlens/actions/runs/42878844758)
passed both native image smoke tests. Anonymous GHCR pull/run and the signed
build attestation were verified after publication. The preliminary `v0.1.0` tag
failed validation before any image or GitHub release was published; it was not
moved or reused.

[Release](../.github/workflows/release.yml) is deterministic GitHub Actions, not a
Copilot Automation. It uses only the repository-scoped `GITHUB_TOKEN`:

1. A pushed stable tag `vMAJOR.MINOR.PATCH` selects the exact source commit.
   Branch refs, prereleases and existing/older releases are rejected.
2. Reusable CI runs Go/race/vet/lint, synthetic relevance, release-script tests,
   workflow lint, desktop/mobile browser checks and the container smoke test.
3. Native runners build amd64 and arm64 images, embed version/revision labels,
   generate SBOM/provenance, push untagged content-addressed images to GHCR and
   smoke-test each exact digest on its native CPU.
4. Only after both smoke tests pass, Actions assembles the manifest and publishes
   `vX.Y.Z`, `X.Y.Z`, `X.Y` and `latest`. Existing version tags are never silently
   replaced with different bytes. A signed GitHub attestation binds the release
   manifest to this workflow.
5. Actions creates a GitHub Release with digest-pinned Compose/environment
   assets, platform digests, notes and SHA-256 checksums.

There are no Paperless/Copilot credentials in CI or image layers. The workflow
has package-write permission only in publication jobs and content-write
permission only in the final GitHub-release job. Actions are pinned by commit.

For a new release, tag the intended validated source commit with the next
unused stable version. For example, after the `v0.1.2` runtime update:

```sh
git tag -a v0.1.3 -m "Paperlens v0.1.3"
git push origin v0.1.3
```

For later releases, use a new version tag on the intended validated source
commit. A release tag can point to the implementation branch without merging
that branch into `main`. Once the workflow is on the default branch, it can also
be manually dispatched **on a version tag**, not on `main` or another branch.
Pushing the tag is the primary trigger and needs no default-branch merge.

Failed publication can be retried with **Re-run failed jobs** so already-tested
platform digests remain the same. Never move a published version tag. If content
must change, create a new patch version. `latest` is mutable; version digests are
the rollback boundary.

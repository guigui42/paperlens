#!/usr/bin/env python3
"""Validate stable releases and generate public, digest-pinned deployment assets."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import sys
import os
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)")
DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
IMAGE = re.compile(r"ghcr\.io/[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*")
REVISION = re.compile(r"[0-9a-f]{40}")

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def check_image_tag(image, tag, expected, opener=None):
    if not IMAGE.fullmatch(image) or not DIGEST.fullmatch(expected):
        raise ValueError("invalid registry image or expected digest")
    version(tag)
    actor, token = os.environ.get("GITHUB_ACTOR", ""), os.environ.get("GH_TOKEN", "")
    if not actor or not token:
        raise ValueError("registry inspection requires the workflow identity")
    opener = opener or urllib.request.build_opener(NoRedirect())
    repository = image.removeprefix("ghcr.io/")
    scope = urllib.parse.urlencode({"service": "ghcr.io", "scope": "repository:" + repository + ":pull"})
    authorization = base64.b64encode((actor + ":" + token).encode()).decode()
    request = urllib.request.Request("https://ghcr.io/token?" + scope,
                                     headers={"Authorization": "Basic " + authorization})
    try:
        with opener.open(request, timeout=30) as response:
            data = json.loads(response.read(65536))
        bearer = data.get("token")
        if not isinstance(bearer, str) or not bearer:
            raise ValueError("registry did not return an inspection token")
        request = urllib.request.Request("https://ghcr.io/v2/" + repository + "/manifests/" + tag,
                                         headers={"Authorization": "Bearer " + bearer,
                                                  "Accept": "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json"})
        try:
            with opener.open(request, timeout=30) as response:
                actual = response.headers.get("Docker-Content-Digest")
        except urllib.error.HTTPError as error:
            if error.code == 404:
                try:
                    codes = {entry.get("code") for entry in json.loads(error.read(65536)).get("errors", [])}
                except (ValueError, AttributeError, TypeError):
                    codes = set()
                if codes == {"MANIFEST_UNKNOWN"}:
                    return
            raise ValueError("registry inspection failed; tag absence was not confirmed") from None
        if actual != expected:
            raise ValueError("refusing to overwrite an existing version tag with different bytes")
    except (urllib.error.URLError, OSError):
        raise ValueError("registry authentication or transport failed; publication stopped") from None


def version(tag):
    match = VERSION.fullmatch(tag)
    if not match:
        raise ValueError("use a stable vMAJOR.MINOR.PATCH tag without leading zeroes")
    return tuple(int(part) for part in match.groups())


def validate_tag(ref_type, tag):
    if ref_type != "tag":
        raise ValueError("release workflows must run on a version tag, not a branch")
    version(tag)


def check_new(tag, releases):
    requested = version(tag)
    for release in releases:
        if release.get("tag_name") == tag:
            raise ValueError("a GitHub release already exists; do not overwrite releases")
        previous = release.get("tag_name", "")
        if release.get("draft") or release.get("prerelease") or not VERSION.fullmatch(previous):
            continue
        if version(previous) >= requested:
            raise ValueError("release version must be newer than every published stable release")


def verify_platforms(index):
    platforms = set()
    for manifest in index.get("manifests", []):
        platform = manifest.get("platform", {})
        os_name, arch = platform.get("os"), platform.get("architecture")
        if os_name == "unknown" and arch == "unknown":
            continue  # BuildKit's SBOM/provenance descriptors are not runnable platforms.
        pair = (os_name, arch)
        if pair in platforms:
            raise ValueError("duplicate runnable platform in release manifest")
        platforms.add(pair)
    if platforms != {("linux", "amd64"), ("linux", "arm64")}:
        raise ValueError("release manifest must contain exactly linux/amd64 and linux/arm64")


def manifest_digest(path):
    data = path.read_bytes()
    if data.endswith(b"\n"):
        data = data[:-1]  # buildx --dry-run adds one newline outside the manifest bytes.
    verify_platforms(json.loads(data))
    return "sha256:" + hashlib.sha256(data).hexdigest()


def assets(tag, image, digest, revision, amd64, arm64, output):
    version(tag)
    if not IMAGE.fullmatch(image):
        raise ValueError("image must be an untagged lowercase ghcr.io/owner/name")
    if not REVISION.fullmatch(revision):
        raise ValueError("revision must be a full Git commit SHA")
    if any(not DIGEST.fullmatch(item) for item in (digest, amd64, arm64)):
        raise ValueError("image references must use full SHA-256 digests")
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError("release asset directory must be empty")
    repo = image.removeprefix("ghcr.io/")
    reference = image + "@" + digest
    (output / "compose.release.yaml").write_text((ROOT / "compose.release.yaml").read_text())
    env = (ROOT / ".env.release.example").read_text()
    env, replaced = re.subn(r"^PAPERLENS_IMAGE=.*$", "PAPERLENS_IMAGE=" + reference, env, flags=re.MULTILINE)
    if replaced != 1:
        raise ValueError("expected exactly one image reference in the deployment environment example")
    (output / "paperlens.env.example").write_text(env)
    (output / "image-reference.txt").write_text(reference + "\n")
    (output / "image-digests.json").write_text(json.dumps({
        "version": tag,
        "revision": revision,
        "image": image,
        "manifest": digest,
        "platforms": {"linux/amd64": amd64, "linux/arm64": arm64},
        "copilot_sdk": "1.0.31",
        "copilot_cli": "1.0.109",
    }, indent=2) + "\n")
    (output / "RELEASE.md").write_text(f"""# Paperlens {tag}

Docker release for Linux amd64 and arm64. Both platform images passed native
non-root, read-only filesystem, readiness, restricted Copilot tool, cleanup and
graceful-shutdown smoke tests. Go/race/lint and desktop/mobile browser checks
passed before image publication.

## Image

```text
{reference}
```

Version tags: `{image}:{tag}`, `{image}:{tag[1:]}`, `{image}:{tag[1:].rsplit('.', 1)[0]}`.
The mutable `latest` tag tracks the newest stable release. Pin the digest above
for reproducible Komodo deployments and rollback.

The image includes Copilot CLI **1.0.109**, paired with Go SDK **1.0.31**.
No Copilot CLI install is needed on the Docker host. Supply credentials only at
runtime through mounted secret files; no household credentials, archive data or
runtime state are included in this release.

## Deployment

Download `compose.release.yaml` and `paperlens.env.example`, copy the latter to
`.env`, set your Paperless origin and host-side credential-file paths, then run:

```sh
sha256sum -c checksums.txt
docker compose -f compose.release.yaml pull
docker compose -f compose.release.yaml up -d
```

Keep the named data volume and both tmpfs mounts. The runtime mount needs `exec`
for the CLI's extracted native addon; upload temporary storage stays `noexec`.
The container runs as UID/GID 10001, which must be able to read the secret files.
The application has no login: keep it on a LAN/VPN or behind an authenticated
proxy. The sample binds loopback and performs no household-stack deployment.

GHCR packages are private on first publication unless the owner explicitly
changes package visibility. For private images, configure GHCR read access on
the Docker/Komodo execution host. Registry authentication is separate from the
Copilot inference credential.

[Docker and Komodo setup](https://github.com/{repo}/blob/{tag}/docs/docker-release.md)
· [Source revision](https://github.com/{repo}/commit/{revision})

## Verification

`image-digests.json` records the release manifest and both platform build digests.
BuildKit SBOM/provenance and a signed GitHub build attestation accompany the
image. To verify provenance with an appropriately authenticated registry client:

```sh
gh attestation verify oci://{reference} --repo {repo}
```

The synthetic relevance results documented in the repository are not a guarantee
of recall on your archive. Inference requires a permitted, entitled identity.
""")
    checksums = []
    for path in sorted(output.iterdir()):
        checksums.append(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name)
    (output / "checksums.txt").write_text("\n".join(checksums) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    validate = commands.add_parser("validate")
    validate.add_argument("ref_type")
    validate.add_argument("tag")
    fresh = commands.add_parser("check-new")
    fresh.add_argument("tag")
    fresh.add_argument("releases", type=Path)
    platforms = commands.add_parser("verify-platforms")
    platforms.add_argument("index", type=Path)
    manifest = commands.add_parser("manifest-digest")
    manifest.add_argument("index", type=Path)
    registry = commands.add_parser("check-image")
    registry.add_argument("image")
    registry.add_argument("tag")
    registry.add_argument("expected")
    generate = commands.add_parser("assets")
    for option in ("tag", "image", "digest", "revision", "amd64", "arm64"):
        generate.add_argument("--" + option, required=True)
    generate.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "validate":
        validate_tag(args.ref_type, args.tag)
    elif args.command == "check-new":
        check_new(args.tag, json.loads(args.releases.read_text()))
    elif args.command == "verify-platforms":
        verify_platforms(json.loads(args.index.read_text()))
    elif args.command == "manifest-digest":
        print(manifest_digest(args.index))
    elif args.command == "check-image":
        check_image_tag(args.image, args.tag, args.expected)
    else:
        assets(args.tag, args.image, args.digest, args.revision, args.amd64, args.arm64, args.output)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError) as error:
        print("Release validation failed: " + str(error), file=sys.stderr)
        sys.exit(1)

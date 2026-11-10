import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("release_assets", Path(__file__).parents[1] / "release-assets.py")
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


class ReleaseTests(unittest.TestCase):
    def test_only_stable_tag_refs_are_allowed(self):
        release.validate_tag("tag", "v0.1.0")
        for ref_type, tag in [
            ("branch", "v0.1.0"), ("tag", "v0.01.0"), ("tag", "v0.1.0-rc.1"),
            ("tag", "v0.1.0;echo bad"), ("tag", "1.0.0"), ("tag", "v1.0.0\n"),
        ]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                release.validate_tag(ref_type, tag)

    def test_releases_are_new_and_monotonic(self):
        existing = [{"tag_name": "v0.1.0", "draft": False, "prerelease": False}]
        release.check_new("v0.2.0", existing)
        for tag in ("v0.1.0", "v0.0.9"):
            with self.assertRaises(ValueError):
                release.check_new(tag, existing)

    def test_exact_native_platforms_and_attestation_descriptors(self):
        index = {"schemaVersion": 2, "manifests": [
            {"platform": {"os": "linux", "architecture": "amd64"}},
            {"platform": {"os": "linux", "architecture": "arm64"}},
            {"platform": {"os": "unknown", "architecture": "unknown"}},
        ]}
        release.verify_platforms(index)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "index.json"
            raw = json.dumps(index).encode()
            path.write_bytes(raw + b"\n")
            self.assertEqual(release.manifest_digest(path), "sha256:" + hashlib.sha256(raw).hexdigest())
        index["manifests"].append(index["manifests"][0])
        with self.assertRaises(ValueError):
            release.verify_platforms(index)
        with self.assertRaises(ValueError):
            release.verify_platforms({"manifests": index["manifests"][:1]})

    def test_public_assets_pin_digests_and_checksums(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "release"
            digest = "sha256:" + "1" * 64
            release.assets("v0.1.0", "ghcr.io/example/paperlens", digest, "a" * 40,
                           "sha256:" + "2" * 64, "sha256:" + "3" * 64, output)
            self.assertEqual((output / "image-reference.txt").read_text(), "ghcr.io/example/paperlens@" + digest + "\n")
            self.assertIn("PAPERLENS_IMAGE=ghcr.io/example/paperlens@" + digest, (output / "paperlens.env.example").read_text())
            checksums = (output / "checksums.txt").read_text().splitlines()
            self.assertEqual(len(checksums), 5)
            for line in checksums:
                checksum, name = line.split("  ")
                self.assertEqual(checksum, hashlib.sha256((output / name).read_bytes()).hexdigest())
            with self.assertRaises(ValueError):
                release.assets("v0.1.0", "ghcr.io/example/paperlens", digest, "a" * 40, digest, digest, output)

    def test_invalid_asset_identifiers_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            for image, digest, revision in [
                ("ghcr.io/example/paperlens:latest", "sha256:" + "1" * 64, "a" * 40),
                ("ghcr.io/example/paperlens", "sha256:truncated", "a" * 40),
                ("ghcr.io/example/paperlens", "sha256:" + "1" * 64, "branch-name"),
            ]:
                with self.assertRaises(ValueError):
                    release.assets("v0.1.0", image, digest, revision, "sha256:" + "2" * 64,
                                   "sha256:" + "3" * 64, Path(directory) / "release")


if __name__ == "__main__":
    unittest.main()

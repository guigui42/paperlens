import importlib.util
import json
from pathlib import Path
import re
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("runtime_release_assets", ROOT / "scripts/release-assets.py")
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


class RuntimePinTests(unittest.TestCase):
    def test_module_image_smoke_and_release_versions_agree(self):
        module = re.search(r"github\.com/github/copilot-sdk/go v([0-9.]+)", (ROOT / "go.mod").read_text()).group(1)
        dockerfile = (ROOT / "Dockerfile").read_text()
        sdk = re.search(r'io\.paperlens\.copilot-sdk\.version="([0-9.]+)"', dockerfile).group(1)
        cli = re.search(r'io\.paperlens\.copilot-cli\.version="([0-9.]+)"', dockerfile).group(1)
        self.assertEqual(module, sdk)
        self.assertIn(f"/releases/download/v{cli}/copilot-linux-", dockerfile)
        self.assertIn(f"GitHub Copilot CLI {cli}.", (ROOT / "scripts/smoke-docker.sh").read_text())
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "release"
            digest = "sha256:" + "1" * 64
            release.assets("v0.1.2", "ghcr.io/example/paperlens", digest, "a" * 40, digest, digest, output)
            metadata = json.loads((output / "image-digests.json").read_text())
            self.assertEqual(metadata["copilot_sdk"], sdk)
            self.assertEqual(metadata["copilot_cli"], cli)
            notes = (output / "RELEASE.md").read_text()
            self.assertIn(f"CLI **{cli}**", notes)
            self.assertIn(f"SDK **{sdk}**", notes)


if __name__ == "__main__":
    unittest.main()

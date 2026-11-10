import importlib.util
import io
from pathlib import Path
import unittest
from unittest.mock import patch
import urllib.error

SPEC = importlib.util.spec_from_file_location("registry_release", Path(__file__).parents[1] / "release-assets.py")
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)
DIGEST = "sha256:" + "1" * 64


class Response(io.BytesIO):
    def __init__(self, value=b"{}", digest=None):
        super().__init__(value)
        self.headers = {"Docker-Content-Digest": digest}


class Registry:
    def __init__(self, result):
        self.result = result

    def open(self, request, timeout):
        if "/token?" in request.full_url:
            return Response(b'{"token":"synthetic"}')
        if isinstance(self.result, Exception):
            raise self.result
        return Response(digest=self.result)


class RegistryGuardTests(unittest.TestCase):
    def inspect(self, response):
        with patch.dict("os.environ", {"GITHUB_ACTOR": "example", "GH_TOKEN": "synthetic"}):
            release.check_image_tag("ghcr.io/example/paperlens", "v0.1.3", DIGEST, Registry(response))

    def test_only_confirmed_missing_manifest_can_publish(self):
        self.inspect(urllib.error.HTTPError("https://ghcr.io/", 404, "missing", {}, io.BytesIO(b'{"errors":[{"code":"MANIFEST_UNKNOWN"}]}')))
        for code, body in [(404, b"not-json"), (404, b'{"errors":[{"code":"NAME_UNKNOWN"}]}'),
                           (401, b"unauthorized"), (403, b"forbidden"), (500, b"error")]:
            with self.subTest(code=code, body=body), self.assertRaises(ValueError):
                self.inspect(urllib.error.HTTPError("https://ghcr.io/", code, "failure", {}, io.BytesIO(body)))
        with self.assertRaises(ValueError):
            self.inspect(urllib.error.URLError("synthetic transport failure"))

    def test_existing_tag_must_match_exactly(self):
        self.inspect(DIGEST)
        with self.assertRaises(ValueError):
            self.inspect("sha256:" + "2" * 64)
        with self.assertRaises(ValueError):
            self.inspect(None)

    def test_resumed_older_publication_rechecks_freshness(self):
        release.check_new("v0.1.3", [{"tag_name": "v0.1.2"}])
        with self.assertRaises(ValueError):
            release.check_new("v0.1.3", [{"tag_name": "v0.1.4"}])


if __name__ == "__main__":
    unittest.main()

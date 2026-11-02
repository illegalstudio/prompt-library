import base64
from contextlib import redirect_stdout
import hashlib
import importlib.util
import io
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("publish_homebrew", Path(__file__).with_name("publish-homebrew.py"))
brew = importlib.util.module_from_spec(spec)
spec.loader.exec_module(brew)


class HomebrewPublicationTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="plib-brew-test-")
        self.addCleanup(self.directory.cleanup)
        self.dist = Path(self.directory.name)
        self.artifacts = {}
        for system, extension in (("darwin", "zip"), ("linux", "tar.gz")):
            for arch in ("arm64", "amd64"):
                name = f"plib_0.1.0_{system}_{arch}.{extension}"
                content = f"final signed archive: {system}/{arch}".encode()
                (self.dist / name).write_bytes(content)
                self.artifacts[name] = hashlib.sha256(content).hexdigest()
        self.formula = brew.render_formula("v0.1.0", self.dist)

    def test_formula_uses_final_archives_and_the_plib_binary(self):
        for name, checksum in self.artifacts.items():
            self.assertIn(f'/v0.1.0/{name}"', self.formula)
            self.assertIn(f'sha256 "{checksum}"', self.formula)
        self.assertIn('depends_on "git"', self.formula)
        self.assertIn('bin.install "plib"', self.formula)
        self.assertIn('#{bin}/plib --version', self.formula)

    def test_generated_formula_is_valid_ruby(self):
        if not shutil.which("ruby"):
            self.skipTest("Ruby is not installed")
        formula = self.dist / "plib.rb"
        formula.write_text(self.formula)
        result = subprocess.run(["ruby", "-c", str(formula)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_first_publication_creates_the_formula(self):
        with patch.object(brew, "gh_json", side_effect=[[], {}]) as gh:
            brew.publish("v0.1.0", self.formula)
        endpoint, payload = gh.call_args_list[-1].args
        self.assertEqual(endpoint, "repos/illegalstudio/homebrew-tap/contents/Formula/plib.rb")
        self.assertNotIn("sha", payload)
        self.assertEqual(base64.b64decode(payload["content"]).decode(), self.formula)
        self.assertEqual(payload["message"], "chore(plib): release v0.1.0")

    def current(self, formula):
        return {"sha": "current-blob-sha", "content": base64.b64encode(formula.encode()).decode()}

    def test_update_uses_the_current_sha(self):
        old = self.formula.replace('version "0.1.0"', 'version "0.0.9"')
        with patch.object(brew, "gh_json", side_effect=[
            [{"path": brew.FORMULA_PATH}], self.current(old), {},
        ]) as gh:
            brew.publish("v0.1.0", self.formula)
        self.assertEqual(gh.call_args_list[-1].args[1]["sha"], "current-blob-sha")

    def test_identical_formula_does_not_create_another_commit(self):
        with patch.object(brew, "gh_json", side_effect=[
            [{"path": brew.FORMULA_PATH}], self.current(self.formula),
        ]) as gh:
            message = brew.publish("v0.1.0", self.formula)
        self.assertIn("up to date", message)
        self.assertEqual(gh.call_count, 2)

    def test_older_release_cannot_downgrade_the_tap(self):
        newer = self.formula.replace('version "0.1.0"', 'version "0.2.0"')
        with patch.object(brew, "gh_json", side_effect=[
            [{"path": brew.FORMULA_PATH}], self.current(newer),
        ]) as gh:
            message = brew.publish("v0.1.0", self.formula)
        self.assertIn("newer", message)
        self.assertEqual(gh.call_count, 2)

    def test_dry_run_never_calls_github(self):
        with patch.object(brew, "gh_json", side_effect=AssertionError("unexpected GitHub call")):
            with redirect_stdout(io.StringIO()) as output:
                result = brew.main(["v0.1.0", "--dist", str(self.dist), "--dry-run"])
        self.assertEqual(result, 0)
        self.assertEqual(output.getvalue(), self.formula)

    def test_missing_final_archive_prevents_publication(self):
        (self.dist / next(iter(self.artifacts))).unlink()
        with self.assertRaises(FileNotFoundError):
            brew.render_formula("v0.1.0", self.dist)

    def test_invalid_versions_are_rejected(self):
        for tag in ("0.1.0", "v01.2.3", "v1.2", "v1.2.3-rc1", "v1.2.3;echo unsafe"):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                brew.render_formula(tag, self.dist)

    def test_failed_reads_are_not_hidden(self):
        error = subprocess.CalledProcessError(1, ["gh", "api"], stderr="HTTP 403 forbidden")
        with patch.object(brew, "gh_json", side_effect=error):
            with self.assertRaises(subprocess.CalledProcessError):
                brew.publish("v0.1.0", self.formula)

    def test_concurrent_updates_are_not_overwritten(self):
        old = self.formula.replace('version "0.1.0"', 'version "0.0.9"')
        error = subprocess.CalledProcessError(1, ["gh", "api"], stderr="HTTP 409 conflict")
        with patch.object(brew, "gh_json", side_effect=[
            [{"path": brew.FORMULA_PATH}], self.current(old), error,
        ]):
            with self.assertRaises(subprocess.CalledProcessError):
                brew.publish("v0.1.0", self.formula)


if __name__ == "__main__":
    unittest.main()

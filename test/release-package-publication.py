#!/usr/bin/env python3
# See LICENSE file in the project root for license information.

"""Exercise the release publisher against a local HTTP package/object store."""

import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import unittest
import urllib.parse


ROOT = Path(__file__).resolve().parents[1]
PUBLISHER = ROOT / ".github/scripts/publish-package-api.sh"
PAYLOAD = b"retained release candidate\n"
CHECKSUM = hashlib.sha256(PAYLOAD).hexdigest()


class PublicationTest(unittest.TestCase):
    def run_case(self, *, metadata=True, stored=None, denied=False,
                 conflict=False, race=None, upload_denied=False, changed_id=False):
        state = {
            "metadata": metadata, "stored": stored, "downloads": 0,
            "claims": 0, "uploads": 0, "condition": None,
        }

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def reply(self, status, body=b"", **headers):
                self.send_response(status)
                self.send_header("Content-Length", str(len(body)))
                for key, value in headers.items():
                    self.send_header(key, value)
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self):
                path = urllib.parse.urlsplit(self.path).path
                if path == "/api/packages":
                    records = [{"id": "candidate", "checksum":
                                "0" * 64 if conflict else CHECKSUM}] if state["metadata"] else []
                    self.reply(200, json.dumps({"total": len(records), "packages": records}).encode())
                elif path == "/api/packages/candidate/download":
                    state["downloads"] += 1
                    if denied or state["stored"] is None:
                        self.reply(403)
                    else:
                        self.reply(200, state["stored"])
                else:
                    self.reply(404)

            def do_PUT(self):
                path = urllib.parse.urlsplit(self.path).path
                if path == "/api/packages":
                    query = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)
                    if query.get("checksum") != [CHECKSUM] or self.headers.get("Authorization") != "Bearer fixture":
                        self.reply(400)
                        return
                    state["claims"] += 1
                    state["metadata"] = True
                    self.reply(307, **{
                        "X-Package-ID": "unexpected" if changed_id else "candidate",
                        "Location": f"http://127.0.0.1:{self.server.server_port}/object",
                    })
                elif path == "/object":
                    state["uploads"] += 1
                    state["condition"] = self.headers.get("If-None-Match")
                    body = self.rfile.read(int(self.headers["Content-Length"]))
                    if upload_denied:
                        self.reply(403)
                        return
                    if race is not None:
                        state["stored"] = race
                    if state["stored"] is not None and state["condition"] == "*":
                        self.reply(412)
                        return
                    state["stored"] = body
                    self.reply(200)
                else:
                    self.reply(404)

        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            archive = work / "out/cmd/rstream/stable/1.2.3/linux/arm64/rstream-1.2.3-linux-arm64.tar.gz"
            archive.parent.mkdir(parents=True)
            archive.write_bytes(PAYLOAD)
            (work / "bin").mkdir()
            # Keep real HTTP/curl behavior, but disable retry delays in failure tests.
            curl = work / "bin/curl"
            curl.write_text("#!/usr/bin/env python3\nimport os,sys\n"
                            "args=sys.argv[1:]\n"
                            "if '--retry' in args: args[args.index('--retry')+1]='0'\n"
                            f"os.execv({shutil.which('curl')!r}, ['curl',*args])\n")
            curl.chmod(0o755)
            server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                result = subprocess.run(
                    ["bash", str(PUBLISHER), "rstream", "stable", "1.2.3",
                     f"http://127.0.0.1:{server.server_port}", "fixture"],
                    cwd=work, env={**os.environ, "PATH": f"{work / 'bin'}:{os.environ['PATH']}"},
                    capture_output=True, text=True, timeout=20,
                )
                state["info"] = Path(str(archive) + ".info").exists()
            finally:
                server.shutdown()
                server.server_close()
                thread.join()
        return result, state

    def test_new_publication(self):
        result, state = self.run_case(metadata=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state["stored"], PAYLOAD)
        self.assertEqual(state["condition"], "*")
        self.assertTrue(state["info"])

    def test_complete_publication_is_read_only(self):
        result, state = self.run_case(stored=PAYLOAD)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((state["claims"], state["uploads"]), (0, 0))

    def test_interrupted_upload_resumes(self):
        result, state = self.run_case()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state["stored"], PAYLOAD)
        self.assertEqual(state["condition"], "*")
        self.assertTrue(state["info"])

    def test_conflicting_metadata_is_not_changed(self):
        result, state = self.run_case(conflict=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((state["claims"], state["uploads"]), (0, 0))

    def test_corrupt_download_is_not_overwritten(self):
        result, state = self.run_case(stored=b"different bytes")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((state["claims"], state["uploads"]), (0, 0))
        self.assertFalse(state["info"])

    def test_download_denial_does_not_allow_overwrite(self):
        result, state = self.run_case(stored=b"existing bytes", denied=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(state["stored"], b"existing bytes")
        self.assertEqual(state["condition"], "*")
        self.assertFalse(state["info"])

    def test_concurrent_identical_upload_is_verified(self):
        result, state = self.run_case(race=PAYLOAD)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state["condition"], "*")
        self.assertTrue(state["info"])

    def test_concurrent_different_upload_is_rejected(self):
        result, state = self.run_case(race=b"other candidate")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(state["stored"], b"other candidate")
        self.assertFalse(state["info"])

    def test_upload_denial_fails_without_metadata_output(self):
        result, state = self.run_case(upload_denied=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIsNone(state["stored"])
        self.assertFalse(state["info"])

    def test_changed_package_identity_is_rejected(self):
        result, state = self.run_case(changed_id=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(state["uploads"], 0)
        self.assertFalse(state["info"])

    def test_workflow_uses_trusted_publication_policy(self):
        workflow = (ROOT / ".github/workflows/promote-release.yml").read_text()
        job = workflow.split("  publish-package-api:\n", 1)[1].split("  publish-debian:\n", 1)[0]
        self.assertIn("run: ./.promotion-policy/.github/scripts/publish-package-api.sh", job)


if __name__ == "__main__":
    unittest.main()

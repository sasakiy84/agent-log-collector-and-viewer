"""Offline release-proof tests: no credentials, network or arbitrary artifact code."""
import copy
import io
import json
import unittest
import zipfile
from urllib.parse import parse_qs, urlsplit

from ci_verification import PROOF, read_proof, verify

REPO = "example/agentlog"
SHA, TREE, HEAD, MERGE, BASE = (c * 40 for c in "abcde")


def zipped(data, filename=PROOF, extra=False):
    stream = io.BytesIO()
    with zipfile.ZipFile(stream, "w") as archive:
        archive.writestr(filename, json.dumps(data))
        if extra:
            archive.writestr("unexpected", "not proof")
    return stream.getvalue()


class Fixture:
    def __init__(self):
        self.proof = {"format": 1, "repository": REPO, "run_id": 7, "run_attempt": 2, "commit": MERGE, "tree": TREE}
        self.run = {"id": 7, "run_attempt": 2, "repository": {"full_name": REPO},
                    "head_repository": {"full_name": REPO}, "workflow_id": 9,
                    "status": "completed", "conclusion": "success", "event": "pull_request", "head_sha": HEAD}
        self.jobs = [{"name": name, "status": "completed", "conclusion": "success"} for name in ("test", "docker", "verified")]
        self.artifact = {"id": 71, "name": "ci-verification-2", "expired": False, "size_in_bytes": 512}
        self.parents = [{"sha": BASE}, {"sha": HEAD}]
        self.ancestor = "identical"
        self.payload = None

    def git(self, *args):
        if args == ("rev-parse", "HEAD"):
            return SHA
        if args == ("rev-parse", "HEAD^{tree}"):
            return TREE
        if args == ("show", "-s", "--format=%P", SHA):
            return BASE + " " + HEAD
        raise AssertionError(args)

    def api(self, path, binary=False):
        route = urlsplit(path).path
        query = parse_qs(urlsplit(path).query)
        if "/compare/" in route:
            return {"status": self.ancestor}
        if route.endswith("workflows/ci.yml"):
            return {"id": 9, "path": ".github/workflows/ci.yml"}
        if route.endswith("workflows/9/runs"):
            return {"workflow_runs": [self.run] if query["head_sha"] == [self.run["head_sha"]] else []}
        if route.endswith("attempts/2/jobs"):
            return {"jobs": self.jobs}
        if route.endswith("runs/7/artifacts"):
            return {"artifacts": [self.artifact]}
        if route.endswith("artifacts/71/zip"):
            assert binary
            return self.payload if self.payload is not None else zipped(self.proof)
        if "/git/commits/" in route:
            return {"tree": {"sha": TREE}, "parents": self.parents}
        raise AssertionError(path)

    def verify(self):
        return verify(REPO, SHA, request=self.api, git_call=self.git)


class ReleaseVerificationTests(unittest.TestCase):
    def test_synthetic_merge_tree_reused(self):
        fixture = Fixture()
        self.assertNotEqual(fixture.proof["commit"], SHA)
        self.assertEqual(fixture.verify(), 7)

    def test_main_exact_commit(self):
        fixture = Fixture()
        fixture.run.update(event="push", head_sha=SHA, head_branch="main")
        fixture.proof["commit"] = SHA
        self.assertEqual(fixture.verify(), 7)

    def test_unmerged_commit_rejected(self):
        fixture = Fixture()
        fixture.ancestor = "diverged"
        with self.assertRaisesRegex(ValueError, "not on main"):
            fixture.verify()

    def test_untrusted_or_incomplete_runs_rejected(self):
        for patch in ({"head_repository": {"full_name": "fork/agentlog"}},
                      {"repository": {"full_name": "fork/agentlog"}},
                      {"workflow_id": 10}, {"conclusion": "failure"},
                      {"event": "pull_request_target"}, {"status": "in_progress"}):
            with self.subTest(patch=patch):
                fixture = Fixture()
                fixture.run.update(patch)
                with self.assertRaisesRegex(ValueError, "No successful CI proof"):
                    fixture.verify()

    def test_required_jobs_must_pass_once(self):
        for mode in ("missing", "skipped", "failed", "duplicate"):
            with self.subTest(mode=mode):
                fixture = Fixture()
                if mode == "missing":
                    fixture.jobs.pop()
                elif mode == "duplicate":
                    fixture.jobs.append(copy.deepcopy(fixture.jobs[0]))
                else:
                    fixture.jobs[1]["conclusion"] = mode
                with self.assertRaises(ValueError):
                    fixture.verify()

    def test_wrong_tree_attempt_run_or_namespace_rejected(self):
        for key, value in (("tree", "0" * 40), ("run_attempt", 1), ("run_id", 8), ("repository", "fork/agentlog")):
            with self.subTest(key=key):
                fixture = Fixture()
                fixture.proof[key] = value
                with self.assertRaises(ValueError):
                    fixture.verify()

    def test_wrong_synthetic_merge_parent_rejected(self):
        fixture = Fixture()
        fixture.parents[1]["sha"] = "0" * 40
        with self.assertRaises(ValueError):
            fixture.verify()

    def test_artifact_must_be_current_bounded_and_available(self):
        for patch in ({"expired": True}, {"name": "ci-verification-1"}, {"size_in_bytes": 65537}):
            with self.subTest(patch=patch):
                fixture = Fixture()
                fixture.artifact.update(patch)
                with self.assertRaises(ValueError):
                    fixture.verify()

    def test_malformed_archive_is_data_never_code(self):
        data = Fixture().proof
        for payload in (zipped(data, "../" + PROOF), zipped(data, extra=True), zipped([[]]),
                        zipped({**data, "extra": "a" * 20000}), b"not a zip", b"a" * 65537):
            with self.subTest(length=len(payload)):
                with self.assertRaises((ValueError, zipfile.BadZipFile)):
                    read_proof(payload)


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Record the tested Git tree; allow releases only from a matching successful CI.

Proof artifacts are bounded JSON, never executable build output. Pull-request
runs are accepted only from this repository; fork changes use main's CI result.
"""
import argparse
import io
import json
import os
import re
import subprocess
import zipfile
from pathlib import Path
from urllib.parse import urlencode

PROOF = "ci-verification.json"
REQUIRED_JOBS = {"test", "docker", "verified"}


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def api(path, binary=False):
    result = subprocess.check_output(["gh", "api", path])
    return result if binary else json.loads(result)


def record(output):
    data = {
        "format": 1,
        "repository": os.environ["GITHUB_REPOSITORY"],
        "run_id": int(os.environ["GITHUB_RUN_ID"]),
        "run_attempt": int(os.environ["GITHUB_RUN_ATTEMPT"]),
        "commit": git("rev-parse", "HEAD"),
        "tree": git("rev-parse", "HEAD^{tree}"),
    }
    Path(output).write_text(json.dumps(data) + "\n")


def read_proof(payload):
    if len(payload) > 65536:
        raise ValueError("proof archive too large")
    with zipfile.ZipFile(io.BytesIO(payload)) as archive:
        files = archive.infolist()
        if len(files) != 1 or files[0].filename != PROOF or files[0].file_size > 16384:
            raise ValueError("unexpected proof archive contents")
        data = json.loads(archive.read(PROOF))
    if not isinstance(data, dict) or set(data) != {"format", "repository", "run_id", "run_attempt", "commit", "tree"}:
        raise ValueError("unexpected proof fields")
    if (type(data["format"]) is not int or data["format"] != 1
            or any(type(data[key]) is not int or data[key] < 1 for key in ("run_id", "run_attempt"))
            or any(not re.fullmatch(r"[0-9a-f]{40}", str(data[key])) for key in ("commit", "tree"))):
        raise ValueError("invalid proof format")
    return data


def trusted_run(run, repository, workflow_id, sha):
    if (run.get("repository", {}).get("full_name") != repository
            or run.get("head_repository", {}).get("full_name") != repository
            or run.get("workflow_id") != workflow_id
            or run.get("status") != "completed" or run.get("conclusion") != "success"):
        return False
    event = run.get("event")
    return event == "pull_request" or (
        event in {"push", "workflow_dispatch"} and run.get("head_sha") == sha
        and (event == "workflow_dispatch" or run.get("head_branch") == "main")
    )


def verify(repository, sha, request=api, git_call=git):
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        raise ValueError("invalid repository")
    if not re.fullmatch(r"[0-9a-f]{40}", sha) or git_call("rev-parse", "HEAD") != sha:
        raise ValueError("release checkout does not match requested commit")
    root = f"repos/{repository}"
    comparison = request(f"{root}/compare/{sha}...main")
    if comparison.get("status") not in {"ahead", "identical"}:
        raise ValueError("release commit is not on main")
    workflow = request(f"{root}/actions/workflows/ci.yml")
    if workflow.get("path") != ".github/workflows/ci.yml":
        raise ValueError("unexpected verification workflow")
    tree = git_call("rev-parse", "HEAD^{tree}")
    # PR checks run on a synthetic merge, while the eventual merge has a new
    # commit hash. Its tested tree, not Actions' run.head_sha tree, is the proof.
    candidates = list(dict.fromkeys([sha, *git_call("show", "-s", "--format=%P", sha).split()]))
    seen, rejected = set(), []
    for head in candidates:
        query = urlencode({"head_sha": head, "status": "success", "per_page": 100})
        runs = request(f"{root}/actions/workflows/{workflow['id']}/runs?{query}")["workflow_runs"]
        for run in runs:
            if run["id"] in seen:
                continue
            seen.add(run["id"])
            if run["head_sha"] != head or not trusted_run(run, repository, workflow["id"], sha):
                continue
            attempt = run["run_attempt"]
            jobs = request(f"{root}/actions/runs/{run['id']}/attempts/{attempt}/jobs?per_page=100")["jobs"]
            names = [job["name"] for job in jobs]
            if any(names.count(name) != 1 for name in REQUIRED_JOBS) or any(
                job["status"] != "completed" or job["conclusion"] != "success"
                for job in jobs if job["name"] in REQUIRED_JOBS
            ):
                continue
            artifact_name = f"ci-verification-{attempt}"
            query = urlencode({"name": artifact_name, "per_page": 100})
            artifacts = request(f"{root}/actions/runs/{run['id']}/artifacts?{query}")["artifacts"]
            artifacts = [a for a in artifacts if a["name"] == artifact_name]
            if len(artifacts) != 1 or artifacts[0]["expired"] or artifacts[0]["size_in_bytes"] > 65536:
                continue
            try:
                proof = read_proof(request(f"{root}/actions/artifacts/{artifacts[0]['id']}/zip", binary=True))
                if (proof["repository"] != repository or proof["run_id"] != run["id"]
                        or proof["run_attempt"] != attempt or proof["tree"] != tree):
                    continue
                commit = request(f"{root}/git/commits/{proof['commit']}")
                if commit["tree"]["sha"] != tree:
                    continue
                if run["event"] == "pull_request":
                    if len(commit["parents"]) != 2 or commit["parents"][1]["sha"] != run["head_sha"]:
                        continue
                elif proof["commit"] != sha:
                    continue
            except (ValueError, KeyError, RuntimeError, zipfile.BadZipFile) as error:
                rejected.append(f"run {run['id']}: {error}")
                continue
            print(f"Verified tree {tree} with CI run {run['id']} attempt {attempt}")
            return run["id"]
    detail = "; ".join(rejected)
    raise ValueError("No successful CI proof matches this tree. Run CI on this ref and retry the release." + (" " + detail if detail else ""))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    writer = commands.add_parser("record")
    writer.add_argument("--output", default=PROOF)
    reader = commands.add_parser("verify")
    reader.add_argument("--repository", default=os.getenv("GITHUB_REPOSITORY"), required=not os.getenv("GITHUB_REPOSITORY"))
    reader.add_argument("--sha", default=os.getenv("GITHUB_SHA"), required=not os.getenv("GITHUB_SHA"))
    args = parser.parse_args()
    try:
        if args.command == "record":
            record(args.output)
        else:
            verify(args.repository, args.sha)
    except (ValueError, subprocess.CalledProcessError) as error:
        parser.exit(1, str(error) + "\n")


if __name__ == "__main__":
    main()

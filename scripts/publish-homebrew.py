#!/usr/bin/env python3
"""Render and publish the release formula through GitHub CLI."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

TAP_REPO = "illegalstudio/homebrew-tap"
FORMULA_PATH = "Formula/plib.rb"
STABLE_VERSION = r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"


def version_tuple(version):
    match = re.fullmatch(STABLE_VERSION, version)
    if not match:
        raise ValueError(f"expected a stable semantic version, got {version!r}")
    return tuple(int(part) for part in match.groups())


def render_formula(tag, dist):
    if not tag.startswith("v"):
        raise ValueError("release tag must start with v")
    version = tag[1:]
    version_tuple(version)
    base = f"https://github.com/illegalstudio/prompt-library/releases/download/{tag}"
    artifacts = {}
    for system, extension in (("darwin", "zip"), ("linux", "tar.gz")):
        for arch in ("arm64", "amd64"):
            name = f"plib_{version}_{system}_{arch}.{extension}"
            digest = hashlib.sha256((dist / name).read_bytes()).hexdigest()
            artifacts[system, arch] = (name, digest)

    formula = [
        "class Plib < Formula",
        '  desc "A prompt library for your terminal"',
        '  homepage "https://github.com/illegalstudio/prompt-library"',
        f'  version "{version}"',
        '  license "MIT"',
        '',
        '  depends_on "git"',
    ]
    for system, block in (("darwin", "on_macos"), ("linux", "on_linux")):
        formula.extend(("", f"  {block} do"))
        for arch, architecture in (("arm64", "on_arm"), ("amd64", "on_intel")):
            name, digest = artifacts[system, arch]
            formula.extend((
                f"    {architecture} do",
                f'      url "{base}/{name}"',
                f'      sha256 "{digest}"',
                "    end",
            ))
        formula.append("  end")
    formula.extend((
        "",
        "  def install",
        '    bin.install "plib"',
        "  end",
        "",
        "  test do",
        '    assert_match version.to_s, shell_output("#{bin}/plib --version")',
        '    assert_match "prompt library", shell_output("#{bin}/plib --help")',
        "  end",
        "end",
        "",
    ))
    return "\n".join(formula)


def gh_json(endpoint, payload=None):
    command = ["gh", "api", endpoint]
    if payload is not None:
        command.extend(("--method", "PUT", "--input", "-"))
    result = subprocess.run(
        command,
        input=json.dumps(payload) if payload is not None else None,
        text=True,
        capture_output=True,
        check=True,
    )
    return json.loads(result.stdout)


def publish(tag, formula):
    directory = f"repos/{TAP_REPO}/contents/Formula"
    endpoint = f"repos/{TAP_REPO}/contents/{FORMULA_PATH}"
    entries = gh_json(directory)
    exists = any(entry["path"] == FORMULA_PATH for entry in entries)
    payload = {
        "message": f"chore(plib): release {tag}",
        "content": base64.b64encode(formula.encode()).decode(),
    }
    if exists:
        current = gh_json(endpoint)
        old_formula = base64.b64decode(current["content"]).decode()
        if old_formula == formula:
            return "Homebrew formula is already up to date"
        match = re.search(r'^  version "([^"]+)"$', old_formula, re.MULTILINE)
        if not match:
            raise ValueError("cannot determine the existing Homebrew formula version")
        if version_tuple(match[1]) > version_tuple(tag[1:]):
            return "A newer Homebrew formula is already published; keeping it"
        # The contents API rejects stale SHAs rather than overwriting a concurrent update.
        payload["sha"] = current["sha"]
    gh_json(endpoint, payload)
    return f"Published {FORMULA_PATH} for {tag} to {TAP_REPO}"


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag", help="Stable release tag, such as v0.1.0")
    parser.add_argument("--dist", type=Path, default=Path("dist"))
    parser.add_argument("--dry-run", action="store_true", help="Print the formula without calling GitHub")
    args = parser.parse_args(argv)
    try:
        formula = render_formula(args.tag, args.dist)
        if args.dry_run:
            print(formula, end="")
        else:
            print(publish(args.tag, formula))
    except subprocess.CalledProcessError as error:
        print(error.stderr or str(error), file=sys.stderr)
        return 1
    except (OSError, ValueError, KeyError) as error:
        print(f"Homebrew publication failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())

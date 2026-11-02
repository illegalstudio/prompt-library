#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
if [[ -n "$(git status --porcelain)" ]]; then
  echo 'error: commit or stash changes before releasing' >&2
  exit 1
fi
git remote get-url origin >/dev/null
git fetch origin --tags
latest="$(git tag -l 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -n1 || true)"
proposed=v0.1.0
if [[ "$latest" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  proposed="v${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.$((BASH_REMATCH[3]+1))"
fi
printf 'Version to release [%s]: ' "$proposed"
read -r input
version="${input:-$proposed}"
[[ "$version" == v* ]] || version="v$version"
if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo 'error: expected a semantic version such as v0.1.0' >&2
  exit 1
fi
if git rev-parse -q --verify "refs/tags/$version" >/dev/null; then
  echo 'error: tag already exists' >&2
  exit 1
fi
branch="$(git symbolic-ref --short HEAD)"
printf 'Release %s from %s (%s), pushing branch and tag to origin. Continue? [y/N] ' "$version" "$branch" "$(git rev-parse --short HEAD)"
read -r confirm
case "$confirm" in y|Y|yes|YES) ;; *) exit 0 ;; esac
git tag -a "$version" -m "Release $version"
# Atomic push ensures the release commit and its tag reach GitHub together.
git push --atomic origin "HEAD:refs/heads/$branch" "refs/tags/$version"
printf 'Pushed %s. GitHub Actions will test, build, sign, notarize and publish the release.\n' "$version"

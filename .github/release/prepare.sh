#!/usr/bin/env bash
set -euo pipefail

version=$(jq -er '."."' .release-please-manifest.json)
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
tag="v$version"
# Published releases are immutable; retries only repair incomplete publication.
if gh api "repos/$GITHUB_REPOSITORY/releases/tags/$tag" > "$RUNNER_TEMP/release.json" 2> "$RUNNER_TEMP/release-error"; then
  published=$(jq -er 'if (.draft | type) == "boolean" then (.draft | not | tostring) else error("Missing draft status") end' "$RUNNER_TEMP/release.json")
else
  grep -q '(HTTP 404)' "$RUNNER_TEMP/release-error"
  published=false
fi
awk -v ver="$version" '/^## \[/ { if (found) exit; if (index($0, "[" ver "]") > 0) found=1 } found' CHANGELOG.md > "$RUNNER_TEMP/release-notes.md"
test -s "$RUNNER_TEMP/release-notes.md"
if git show-ref --verify --quiet "refs/tags/$tag"; then
  [[ "$(git rev-parse "$tag^{commit}")" == "$RELEASE_COMMIT" ]]
else
  git tag "$tag" "$RELEASE_COMMIT"
  git push origin "refs/tags/$tag"
fi
printf 'published=%s\ntag=%s\n' "$published" "$tag" >> "$GITHUB_OUTPUT"

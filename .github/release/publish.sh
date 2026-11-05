#!/usr/bin/env bash
set -euo pipefail

expected=$(cat "$RUNNER_TEMP/release-notes.md")
if [[ ! "$expected" =~ [^[:space:]] ]]; then
  echo 'Release notes must not be empty.' >&2
  exit 1
fi

# Use gh release view to find drafts; the release-by-tag API returns 404 for them.
release=$(gh release view "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --json tagName,isDraft,body)
jq -e --arg tag "$RELEASE_TAG" '
  .tagName == $tag and .isDraft == true and (.body | type) == "string"
' <<< "$release" >/dev/null
actual=$(jq -r .body <<< "$release")
# GoReleaser may append newlines; command substitution trims them on both sides.
if [[ "$actual" != "$expected" ]]; then
  echo 'Draft release description does not match the prepared release notes.' >&2
  exit 1
fi

gh release edit "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --draft=false

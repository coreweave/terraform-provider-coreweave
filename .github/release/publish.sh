#!/usr/bin/env bash
set -euo pipefail

expected=$(cat "$RUNNER_TEMP/release-notes.md")
if [[ ! "$expected" =~ [^[:space:]] ]]; then
  echo 'Release notes must not be empty.' >&2
  exit 1
fi

release=$(gh api "repos/$GITHUB_REPOSITORY/releases/tags/$RELEASE_TAG")
jq -e --arg tag "$RELEASE_TAG" '
  .tag_name == $tag and .draft == true and (.body | type) == "string"
' <<< "$release" >/dev/null
actual=$(jq -r .body <<< "$release")
# GoReleaser may append newlines; command substitution trims them on both sides.
if [[ "$actual" != "$expected" ]]; then
  echo 'Draft release description does not match the prepared release notes.' >&2
  exit 1
fi

gh release edit "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --draft=false

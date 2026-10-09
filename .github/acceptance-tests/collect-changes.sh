#!/usr/bin/env bash
set -euo pipefail

# File coverage follows the tested merge tree; scopes come only from PR commits.
git diff --no-renames --name-only "$BASE_SHA...$GITHUB_SHA" > "$1/files.txt"
git log --format=%s "$BASE_SHA..$HEAD_SHA" > "$1/subjects.txt"

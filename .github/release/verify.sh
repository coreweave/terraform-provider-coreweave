#!/usr/bin/env bash
set -euo pipefail

[[ "$PR_NUMBER" =~ ^[1-9][0-9]*$ ]]
pr=$(gh api "repos/$GITHUB_REPOSITORY/pulls/$PR_NUMBER")
jq -e --arg repo "$GITHUB_REPOSITORY" '
  .merged == true and .state == "closed" and
  .head.ref == "release-please--branches--main" and .base.ref == "main" and
  .head.repo.full_name == $repo and .base.repo.full_name == $repo
' <<< "$pr" >/dev/null
head=$(jq -r .head.sha <<< "$pr")
commit=$(jq -r .merge_commit_sha <<< "$pr")
[[ "$head" =~ ^[a-f0-9]{40}$ && "$commit" =~ ^[a-f0-9]{40}$ ]]

# Wait only for Tests: publication's own checks can still be pending on this PR.
run=$(gh run list --repo "$GITHUB_REPOSITORY" --workflow test.yml --commit "$head" --event pull_request --limit 1 --json databaseId --jq '.[0].databaseId // empty')
[[ "$run" =~ ^[1-9][0-9]*$ ]]
gh run watch "$run" --repo "$GITHUB_REPOSITORY" --exit-status
jobs=$(gh run view "$run" --repo "$GITHUB_REPOSITORY" --json jobs)
# Ordinary PR validation alone does not prove that full acceptance ran.
jq -e '
  [.jobs[] | select(.name == "Release acceptance")] |
  length == 1 and .[0].conclusion == "success"
' <<< "$jobs" >/dev/null

# Reject changes introduced at merge that were absent from the tested release candidate.
head_tree=$(gh api "repos/$GITHUB_REPOSITORY/git/commits/$head" --jq .tree.sha)
merge_tree=$(gh api "repos/$GITHUB_REPOSITORY/git/commits/$commit" --jq .tree.sha)
[[ "$head_tree" =~ ^[a-f0-9]{40}$ && "$head_tree" == "$merge_tree" ]]
printf 'commit=%s\n' "$commit" >> "$GITHUB_OUTPUT"

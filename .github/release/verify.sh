#!/usr/bin/env bash
set -euo pipefail

case "$GITHUB_EVENT_NAME" in
  push)
    [[ "$GITHUB_SHA" =~ ^[a-f0-9]{40}$ ]]
    # The association list uses merged_at. Recheck the selected PR through its
    # own endpoint before trusting it as a release candidate.
    PR_NUMBER=$(gh api --paginate "repos/$GITHUB_REPOSITORY/commits/$GITHUB_SHA/pulls" |
      jq -sr --arg repo "$GITHUB_REPOSITORY" --arg sha "$GITHUB_SHA" '
        [.[][] | select(
          .state == "closed" and .merged_at != null and .merge_commit_sha == $sha and
          .head.ref == "release-please--branches--main" and .base.ref == "main" and
          .head.repo.full_name == $repo and .base.repo.full_name == $repo
        )] |
        if length == 0 then ""
        elif length == 1 then .[0].number
        else error("Multiple merged release PRs for the pushed commit")
        end
      ')
    if [[ -z "$PR_NUMBER" ]]; then
      echo "No merged release PR for the pushed commit."
      exit 0
    fi
    ;;
  workflow_dispatch) ;;
  *) exit 1 ;;
esac

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
if [[ "$GITHUB_EVENT_NAME" == push ]]; then
  [[ "$commit" == "$GITHUB_SHA" ]]
fi

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
printf 'commit=%s\npr-number=%s\n' "$commit" "$PR_NUMBER" >> "$GITHUB_OUTPUT"

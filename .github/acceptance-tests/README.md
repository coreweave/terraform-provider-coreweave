# Release acceptance

The `Tests` workflow runs for every PR when opened, updated, or reopened. Ordinary PRs, including Renovate dependency updates, run compilation, lint, module consistency, unit tests, documentation generation, snapshot builds, and title validation. They skip acceptance tests.

Every release-please PR from this repository's `release-please--branches--main` branch to `main` runs all acceptance suites: caller identity, CKS, networking, object storage, inference, and container registry. This tests the accumulated changes even when the release PR itself only updates the manifest and changelog. Changed paths, labels, titles, and commit scopes do not select QA suites.

Selection fetches current PR metadata and requires an open PR at the requested head SHA. A stale rerun, closed PR, incomplete metadata, or lookup failure fails selection. Forks and similarly named branches do not qualify as release candidates.

Release Please creates and updates PRs with the repository's `GITHUB_TOKEN`. GitHub holds the resulting `pull_request` workflows for human approval. On the release PR, click **Approve workflows to run** in the checks/merge area to start `Tests`, including its full six-suite acceptance matrix. No separate environment approval or navigation to workflow runs is needed. Each automation-triggered PR revision requires approval. Human pushes, **Update branch**, or reopening the release PR can start workflows without that prompt; use those actions only when you intend to start QA. See [GitHub's workflow triggering documentation](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow).

After approval, acceptance waits for compilation, lint, module consistency, and unit tests. Snapshot builds and documentation generation remain parallel. Each suite holds its shared `acceptance-test-<suite>` lock; queued arrivals do not cancel running sweeps or tests. Before checkout and each resource command, QA verifies that the PR is still open at the requested revision. Started commands finish their cleanup; outdated queued commands fail before using resources.

The stable `PR validation` check uses `alls-green` to aggregate hosted checks and the acceptance matrix. It permits the acceptance skip only for ordinary PRs. The separate `Release acceptance` check runs only for a release candidate and requires successful full acceptance and `PR validation`, with no permitted skips.

## Publication

Release Please only manages release PRs; it does not create tags or GitHub releases. Its built-in token enables the PR-level workflow approval described above. Renovate continues to use its existing App credentials, so routine dependency PRs run their fast checks automatically. QA suites continue to use the existing `QA` environment and credentials.

When a release PR merges, `Release` verifies its identity and waits for the candidate's `Tests` run using `gh run watch`. It requires that run's `Release acceptance` job to succeed, then verifies that the merged commit's tree matches the tested PR head. Missing, failed, skipped, canceled, or stale validation blocks publication. The workflow cannot prevent an early merge when required-check settings are absent. Keep the release PR open until `Release acceptance` succeeds so queued QA phases can pass their freshness guards. Commands already running may finish after merge; publication still waits for full successful validation of the identical candidate tree.

Publication checks out that exact merge commit, reads the version and notes there, and refuses to move an existing tag. GoReleaser builds and signs a draft release; the workflow publishes it and marks the PR tagged only after successful completion, then refreshes release-please to pick up any later changes on `main`. Retrying a completed release leaves its assets unchanged.

To recover an interrupted publication, manually run `Release` on `main` with the merged release PR number. Recovery uses the same acceptance gate. It cannot repair QA phases that failed their freshness guards because the PR was already closed; that case requires a new open candidate. In a repair PR, revert the blocked release commit's manifest and changelog updates while retaining the provider changes. After merging that repair, remove `autorelease: pending` from the blocked release PR and manually run `Release Please` on `main`. It can then propose that still-unreleased version again, and the new PR must pass full QA before merging. Historical releases without the new release acceptance check also cannot use this recovery path.

Run the secret-free workflow fixtures locally with:

```sh
node --test .github/acceptance-tests/*.test.mjs .github/release/*.test.mjs
```

# Release acceptance

The `Tests` workflow runs for every PR when opened, updated, or reopened. Ordinary PRs, including Renovate dependency updates, run compilation, lint, module consistency, unit tests, documentation generation, snapshot builds, and title validation. They skip acceptance tests.

Every release-please PR from this repository's `release-please--branches--main` branch to `main` runs all acceptance suites: caller identity, CKS, networking, object storage, inference, and container registry. This tests the accumulated changes even when the release PR itself only updates the manifest and changelog. Changed paths, labels, titles, and commit scopes do not select QA suites.

Selection fetches current PR metadata and requires an open PR at the requested head SHA. A stale rerun, closed PR, incomplete metadata, or lookup failure fails selection. Forks and similarly named branches do not qualify as release candidates.

Acceptance waits for compilation, lint, module consistency, and unit tests, then pauses at `Approve release acceptance`. Open the release PR's `Tests` run, click **Review deployments**, select `release-acceptance`, and click **Approve and deploy**. This one approval unlocks all six suites, including their sweeps; it does not publish a release. Every new PR revision has its own workflow run and approval checkpoint. Rejection blocks acceptance and publication. Snapshot builds and documentation generation remain parallel. Each suite holds its shared `acceptance-test-<suite>` lock; queued arrivals do not cancel running sweeps or tests. Before checkout and each resource command, QA verifies that the PR is still open at the requested revision. Started commands finish their cleanup; outdated queued commands fail before using resources.

The stable `PR validation` check uses `alls-green` to aggregate hosted checks and the acceptance matrix. It permits skipping the approval checkpoint and acceptance only for ordinary PRs. The separate `Release acceptance` check runs only for a release candidate and requires successful full acceptance and `PR validation`, with no permitted skips.

## Publication

Release Please uses the existing repository-scoped App credentials to create and update PRs so those updates trigger CI. It does not create tags or GitHub releases. The `release-acceptance` environment has a required reviewer for the human checkpoint. This environment contains no QA secrets; the existing `QA` environment continues to supply the suite credentials. Renovate authentication and the existing App credentials are unchanged.

When a release PR merges, `Release` verifies its identity and waits for the candidate's `Tests` run using `gh run watch`. It requires that run's `Release acceptance` job to succeed, then verifies that the merged commit's tree matches the tested PR head. Missing, failed, skipped, canceled, or stale validation blocks publication. The workflow cannot prevent an early merge when required-check settings are absent. Keep the release PR open until `Release acceptance` succeeds so queued QA phases can pass their freshness guards. Commands already running may finish after merge; publication still waits for full successful validation of the identical candidate tree.

Publication checks out that exact merge commit, reads the version and notes there, and refuses to move an existing tag. GoReleaser builds and signs a draft release; the workflow publishes it and marks the PR tagged only after successful completion, then refreshes release-please to pick up any later changes on `main`. Retrying a completed release leaves its assets unchanged.

To recover an interrupted publication, manually run `Release` on `main` with the merged release PR number. Recovery uses the same acceptance gate. It cannot repair QA phases that failed their freshness guards because the PR was already closed; that case requires a new open candidate. In a repair PR, revert the blocked release commit's manifest and changelog updates while retaining the provider changes. After merging that repair, remove `autorelease: pending` from the blocked release PR and manually run `Release Please` on `main`. It can then propose that still-unreleased version again, and the new PR must pass full QA before merging. Historical releases without the new release acceptance check also cannot use this recovery path.

Run the secret-free workflow fixtures locally with:

```sh
node --test .github/acceptance-tests/*.test.mjs .github/release/*.test.mjs
```

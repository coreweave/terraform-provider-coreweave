# Acceptance suite selection

The `Tests` workflow runs when a PR is opened, updated, or reopened, including documentation-only PRs. Acceptance selection runs alongside hosted validation. The `acceptance-tests` QA matrix waits for successful compilation, lint, and the job that checks `go mod tidy` and unit tests. A failure, cancellation, or skipped prerequisite prevents any QA suite from being queued. Snapshot builds and documentation generation remain parallel checks.

Changing labels alone does not launch QA work. To request another suite, add `acceptance-test:<suite>` or `acceptance-test:all`, then rerun **all jobs** in that PR's `Tests` run. Rerunning only failed jobs can retain the previous suite selection.

Selection fetches the current PR title, labels, state, and head SHA. A rerun for an older revision or a closed PR fails selection before launching the QA matrix. Lookup failures also fail selection. The job summary lists the selected suites and the requested head SHA; an empty selection means no suites were applicable, not that acceptance tests passed.

Root module files, shared clients, test helpers, and provider changes select every suite.

Individual suites are selected by service directories, acceptance labels, or conventional commit scopes in the current PR title and commit subjects.

Changes limited to Markdown, `docs/`, `examples/`, or `LICENSE` select no acceptance suites. Suite labels and scopes do not override this exclusion. Workflow-only changes select none unless a suite label or scope requests testing.

Changed files are compared against the tested merge revision, so updates already present on the base branch do not request extra suites. Moves between service directories select both the source and destination suites. Commit scopes come only from the PR's own commits.

Each suite holds the shared `acceptance-test-<suite>` lock across PRs. Up to 100 pending runs are retained; additional arrivals are canceled when the queue is full. New arrivals do not cancel a running sweep or test.

After acquiring the lock, each QA job fetches current PR metadata before checking out repository code and again immediately before its sweep or test command. Closed or outdated revisions, malformed metadata, and API lookup failures stop that phase with a failure instead of reporting untested acceptance as successful. A sweep already in progress finishes; if its PR changed, the test job rejects the outdated revision. A test already in progress finishes for its original revision.

The `PR validation` check waits for every hosted check and the acceptance matrix, then checks their native job results. All hosted checks must succeed. The acceptance matrix must succeed when suites are selected; failed, canceled, or skipped acceptance fails validation.

For an empty suite selection, acceptance must be skipped and the summary reports it as not applicable. This validates the hosted checks without claiming that QA tests ran.

Freshness checks are snapshots, so the PR can still change after a check. Completed-result reuse and deduplication remain separate scheduling safeguards. Queue behavior and applicable-suite aggregation will also need confirmation from normal acceptance runs after deployment; local fixtures do not exercise GitHub's scheduler. The aggregate reports results; merge blocking requires required-check settings, which this workflow does not configure.

Run the secret-free selection fixtures locally with:

```sh
node --test .github/acceptance-tests/*.test.mjs
```

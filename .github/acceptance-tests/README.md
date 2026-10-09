# Acceptance suite selection

Acceptance runs start when a PR is opened, updated, or reopened. Changing labels alone does not launch QA work. To request another suite, add `acceptance-test:<suite>` or `acceptance-test:all`, then rerun **all jobs** in that PR's Acceptance Tests run. Rerunning only failed jobs can retain the previous suite selection.

Selection fetches the current PR title, labels, state, and head SHA. A rerun for an older revision or a closed PR fails selection before launching the QA matrix. Lookup failures also fail selection. The job summary lists the selected suites and the requested head SHA; an empty selection means no suites were applicable, not that acceptance tests passed.

Root module files, shared clients, test helpers, and provider changes select every suite. Service directories, conventional commit scopes in the current title or commit subjects, and acceptance labels add individual suites. Documentation and workflow changes alone select none.

Changed files are compared against the tested merge revision, so updates already present on the base branch do not request extra suites. Moves between service directories select both the source and destination suites. Commit scopes come only from the PR's own commits.

Each suite holds the shared `acceptance-test-<suite>` lock across PRs. Up to 100 pending runs are retained; additional arrivals are canceled when the queue is full. New arrivals do not cancel a running sweep or test.

After acquiring the lock, each QA job fetches current PR metadata before checking out repository code and again immediately before its sweep or test command. Closed or outdated revisions, malformed metadata, and API lookup failures stop that phase with a failure instead of reporting untested acceptance as successful. A sweep already in progress finishes; if its PR changed, the test job rejects the outdated revision. A test already in progress finishes for its original revision.

Freshness checks are snapshots, so the PR can still change after a check. Fast-check gating, deduplication, and a current-revision aggregate remain separate scheduling safeguards. Queue behavior will also need confirmation from normal acceptance runs after deployment; local fixtures do not exercise GitHub's scheduler.

Run the secret-free selection fixtures locally with:

```sh
node --test .github/acceptance-tests/*.test.mjs
```

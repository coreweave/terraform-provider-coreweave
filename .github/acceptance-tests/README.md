# Acceptance suite selection

Acceptance runs start when a PR is opened, updated, or reopened. Changing labels alone does not launch QA work. To request another suite, add `acceptance-test:<suite>` or `acceptance-test:all`, then rerun **all jobs** in that PR's Acceptance Tests run. Rerunning only failed jobs can retain the previous suite selection.

Selection fetches the current PR title, labels, state, and head SHA. A rerun for an older revision or a closed PR fails selection before launching the QA matrix. Lookup failures also fail selection. The job summary lists the selected suites and the requested head SHA; an empty selection means no suites were applicable, not that acceptance tests passed.

Root module files, shared clients, test helpers, and provider changes select every suite. Service directories, conventional commit scopes in the current title or commit subjects, and acceptance labels add individual suites. Documentation and workflow changes alone select none.

Changed files are compared against the tested merge revision, so updates already present on the base branch do not request extra suites. Moves between service directories select both the source and destination suites. Commit scopes come only from the PR's own commits.

This check happens during selection, before queueing. A PR can still change while QA work is queued or running. Suite queue retention, checks after acquiring the suite lock, fast-check gating, and a current-revision aggregate are separate scheduling safeguards.

Run the secret-free selection fixtures locally with:

```sh
node --test .github/acceptance-tests/*.test.mjs
```

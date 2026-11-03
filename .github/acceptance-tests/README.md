# Release acceptance

See [Acceptance testing](../../ACCEPTANCE_TESTING.md) for local commands, naming, sweep safety, and non-blocking acceptance layout.

> **Observability suite failing? Tell the Telemetry Relay team in `#wg-telemetry-relay`.**
>
> The `observability` suites exercise a live telecasterd and the Kafka Connect clusters behind it, so a failure is as likely to be a server-side or environment problem as a provider bug — a pipeline stuck in `FORWARDING_PIPELINE_STATE_PENDING`, a connector that never starts, or an API that stops answering are all theirs to diagnose, not yours. Post the failing subtest name, the slug, and the run log before assuming the test is at fault.

The `Tests` workflow runs for every PR when opened, updated, or reopened. Ordinary PRs, including Renovate dependency updates, run compilation, lint, module consistency, unit tests, documentation generation, snapshot builds, and title validation. They skip acceptance tests.

Every release-please PR from this repository's `release-please--branches--main` branch to `main` runs all acceptance suites: caller identity, CKS, networking, object storage, inference, and container registry. This tests the accumulated changes even when the release PR itself only updates the manifest and changelog. Changed paths, labels, titles, and commit scopes do not select QA suites.

Selection fetches current PR metadata and requires an open PR at the requested head SHA. A stale rerun, closed PR, incomplete metadata, or lookup failure fails selection. Forks and similarly named branches do not qualify as release candidates.

## Manual runs

Because selection only fires for the release PR, and the release PR is created from changes already on `main`, a suite is never exercised against code that is still in review. `Acceptance (manual)` closes that gap.

Run it from the Actions tab, or:

```sh
gh workflow run acceptance-manual.yaml --ref <branch> -f suite=observability
gh workflow run acceptance-manual.yaml --ref <branch> -f suite=observability -f run=TestForwardingEndpointHTTPS
```

It is `workflow_dispatch` only. Dispatch requires write access and can only select a ref that exists in this repository, so a fork's pull request branch cannot reach it — which matters because this repository is public. The workflow must exist on the default branch before it appears; dispatching a ref runs that ref's copy.

It does not reuse `acceptance-test.yaml`. That workflow's freshness guard requires a `pull_request` event and rejects every other event, which `current-pr.test.mjs` pins; routing a dispatch through it would mean weakening that guard.

Credentials come from the `QA` environment, with the suite carrying its own secret — `COREWEAVE_OBSERVABILITY_API_TOKEN` — the way object_storage already carries `COREWEAVE_OBJS_API_TOKEN`. Both manual observability runs and release acceptance use this token; a missing suite-specific secret fails closed rather than falling back to the shared `COREWEAVE_API_TOKEN` organization.

That token must belong to an organization whose registered clusters are **lab-only**. This is what makes the pipeline suites safe to run: a pipeline creates one connector per source Kafka cluster holding the stream's topics, so an organization with a production cluster footprint would provision real connectors in a production zone.

The organization that satisfies this is **`q8e412`**, the Telemetry Relay QA testing organization. It exists for this purpose and has only lab-only clusters registered, and `COREWEAVE_OBSERVABILITY_API_TOKEN` is minted for it. A local run should use a token from `q8e412` as well: the suites generate slugs prefixed `test-acc-`, and the sweeper deletes every endpoint and pipeline carrying that prefix in whatever organization the token belongs to, so a token for a shared organization sweeps that organization's test resources too. A local run holds no `acceptance-test-<suite>` lock, so check for an in-flight manual or release run before starting one.

Because this shares the `QA` environment with release acceptance, a dispatched branch can read that environment's other secrets. Dispatch requires write access, and that is the control being relied on.

### Token rotation

`COREWEAVE_OBSERVABILITY_API_TOKEN` expires **2027-10-01**. Rotate it before then: mint a replacement in the QA console for the same organization (`q8e412`) and overwrite the secret in the `QA` environment. Nothing else changes, and no workflow edit is needed.

An expired token does not fail loudly in a way that points at the cause — the suites fail on `401` responses from the first API call, which reads as a broken test rather than a lapsed credential. If a previously green suite starts failing wholesale on authentication, check the expiry first.

To confirm an organization is lab-only before trusting it, list its streams and check every `zones_active` entry:

```sh
curl -s -X POST "$COREWEAVE_API_ENDPOINT/coreweave.telemetryrelay.svc.cluster.v1beta1.TelemetryRelayService/ListStreams" \
  -H "Authorization: Bearer $COREWEAVE_API_TOKEN" \
  -H 'Content-Type: application/json' -H 'Connect-Protocol-Version: 1' -d '{}'
```

The `Connect-Protocol-Version` header is required. Without it the request 404s, and the 404 is reported as `TELEMETRYRELAY_ENDPOINT_NOT_FOUND`, which reads like a missing forwarding endpoint rather than a missing route.

Release Please creates and updates PRs with the repository's `GITHUB_TOKEN`. GitHub holds the resulting `pull_request` workflows for human approval. On the release PR, click **Approve workflows to run** in the checks/merge area to start `Tests`, including its full seven-suite acceptance matrix. No separate environment approval or navigation to workflow runs is needed. Each automation-triggered PR revision requires approval. Human pushes, **Update branch**, or reopening the release PR can start workflows without that prompt; use those actions only when you intend to start QA. See [GitHub's workflow triggering documentation](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow).

After approval, acceptance waits for compilation, lint, module consistency, and unit tests. Snapshot builds and documentation generation remain parallel.

Each suite holds its shared `acceptance-test-<suite>` lock; queued arrivals do not cancel running sweeps or tests. The `sweep` job runs first. The normal `test` job and the `nonblocking` job then run in parallel, each depending only on `sweep`. No other job runs a sweep. The lock stays held until both test jobs finish.

Before checkout and each resource command, QA verifies that the PR is still open at the requested revision. Started commands finish their cleanup; outdated queued commands fail before using resources.

The stable `PR validation` check uses `alls-green` to aggregate hosted checks and the acceptance matrix. It permits the acceptance skip only for ordinary PRs. The separate `Release acceptance` check runs only for a release candidate and requires successful full acceptance and `PR validation`, with no permitted skips.

## Non-blocking acceptance

Non-blocking acceptance tests exercise scenarios that do not currently gate merges or releases. Maintainers review their actual results and move suitable cases into required acceptance coverage.

`make testacc-nonblocking` enables the `nonblocking` build tag, selects `TestNonBlockingAcceptance`, and saves Go JSON for a step summary. Normal tests need no exclusion filter. Test bodies stay in their original files as lowercase helpers; only the runner lives in a tagged file. The summary lists each package, scenario, and result. A passing case gets a reminder to review it for required acceptance coverage. A suite with no group passes with no tests to run.

The non-blocking job uses job-level `continue-on-error: true`. It posts its real outcome as a separate `Non-blocking acceptance (<suite>)` check through the Checks API. Keep that check informational in branch protection. Normal tests and sweeps remain blocking. `Release acceptance` doesn't directly depend on the non-blocking job.

Non-blocking test failures do not fail `acceptance-tests`, `PR validation`, `Release acceptance`, or the overall `Tests` workflow. Publication can proceed because it waits on that workflow with `gh run watch --exit-status`. Follow the [throwaway-PR verification steps](../../ACCEPTANCE_TESTING.md#confirm-behavior-on-a-throwaway-pr) after changing this behavior. Blocking CI also compiles the tagged tests without running them, and lint includes the tagged runner.

## Publication

Release Please only manages release PRs; it does not create tags or GitHub releases. Its built-in token enables the PR-level workflow approval described above. Renovate continues to use its existing App credentials, so routine dependency PRs run their fast checks automatically. QA suites continue to use the existing `QA` environment and credentials.

When a release PR merges, `Release` verifies its identity and waits for the candidate's `Tests` run using `gh run watch`. It requires that run's `Release acceptance` job to succeed, then verifies that the merged commit's tree matches the tested PR head. Missing, failed, skipped, canceled, or stale validation blocks publication. The workflow cannot prevent an early merge when required-check settings are absent. Keep the release PR open until `Release acceptance` succeeds so queued QA phases can pass their freshness guards. Commands already running may finish after merge; publication still waits for full successful validation of the identical candidate tree.

Publication checks out that exact merge commit, reads the version and notes there, and refuses to move an existing tag. GoReleaser builds and signs a draft release; the workflow publishes it and marks the PR tagged only after successful completion, then refreshes release-please to pick up any later changes on `main`. Retrying a completed release leaves its assets unchanged.

To recover an interrupted publication, manually run `Release` on `main` with the merged release PR number. Recovery uses the same acceptance gate. It cannot repair QA phases that failed their freshness guards because the PR was already closed; that case requires a new open candidate. In a repair PR, revert the blocked release commit's manifest and changelog updates while retaining the provider changes. After merging that repair, remove `autorelease: pending` from the blocked release PR and manually run `Release Please` on `main`. It can then propose that still-unreleased version again, and the new PR must pass full QA before merging. Historical releases without the new release acceptance check also cannot use this recovery path.

Run the secret-free workflow fixtures locally with:

```sh
node --test .github/acceptance-tests/*.test.mjs .github/release/*.test.mjs
```

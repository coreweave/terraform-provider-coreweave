# Release acceptance

Local commands, naming and sweep safety: [Acceptance testing](../../ACCEPTANCE_TESTING.md).

> **Observability suite failing? Post in `#wg-telemetry-relay`** with the failing subtest, the slug and the run log.
>
> Only `TestForwardingPipeline` reaches Kafka Connect, so a connector that never starts or a pipeline stuck in `FORWARDING_PIPELINE_STATE_PENDING` is theirs. Endpoints are database-only records server-side and `CheckEndpoint` runs no connectivity test, so endpoint and stream failures are usually the provider or the API itself.

## When acceptance runs

| PR | What runs |
|---|---|
| Ordinary, including Renovate | compile, lint, module consistency, unit tests, docs, snapshot build, title check — no acceptance |
| `release-please--branches--main` → `main` | all seven suites: `caller_identity`, `cks`, `networking`, `object_storage`, `inference`, `container_registry`, `observability` |

Paths, labels, titles and commit scopes never select suites. Selection requires an open PR at the requested head SHA; stale reruns, closed PRs, incomplete metadata and forks all fail it.

On a release PR, click **Approve workflows to run** — Release Please pushes with `GITHUB_TOKEN`, so GitHub holds its workflows. Every automation-pushed revision needs approving again; human pushes, **Update branch** and reopening start QA without asking, so use them only when you mean to.

## Manual runs

Release acceptance only tests code already on `main`, so nothing in review gets a suite. `Acceptance (manual)` closes that gap:

```sh
gh workflow run acceptance-manual.yaml --ref <branch> -f suite=observability
gh workflow run acceptance-manual.yaml --ref <branch> -f suite=observability -f run=TestForwardingEndpointHTTPS
```

`workflow_dispatch` only; needs write access and a ref in this repository, so a fork's branch cannot reach it — which matters because this repository is public. It deliberately does not reuse `acceptance-test.yaml`, whose freshness guard requires a `pull_request` event (pinned by `current-pr.test.mjs`).

A dispatched branch can read every other `QA` secret. Write access is the control being relied on.

## Credentials

| | |
|---|---|
| Secret | `COREWEAVE_API_TOKEN`, in the `QA` environment — the same secret every suite except `object_storage` uses |
| Organization | must have **exactly one** registered cluster, in `US-LAB-01A` |
| If absent | the suite fails on the first API call |

**The organization must be lab-only, and must have a registration.** A pipeline creates one connector per source Kafka cluster holding the stream's topics, so an organization with a production cluster footprint would get real connectors in a production zone. An organization with no registered clusters at all fails `CreatePipeline`'s precondition, so the suite cannot run there either.

An expired token fails as `401` on the first API call, which reads as a broken test rather than a lapsed credential — check the credential before debugging a suite that was green yesterday.

Local runs need a `q8e412` token too: the sweeper deletes every endpoint and pipeline prefixed `test-acc-` in whatever organization the token belongs to. They also hold no `acceptance-test-<suite>` lock, so check for an in-flight manual or release run first.

Confirm an organization is lab-only by listing its streams and checking every `zones_active`:

```sh
curl -s -X POST "$COREWEAVE_API_ENDPOINT/coreweave.telemetryrelay.svc.cluster.v1beta1.TelemetryRelayService/ListStreams" \
  -H "Authorization: Bearer $COREWEAVE_API_TOKEN" \
  -H 'Content-Type: application/json' -H 'Connect-Protocol-Version: 1' -d '{}'
```

`Connect-Protocol-Version` is required. Without it the request 404s and the 404 surfaces as `TELEMETRYRELAY_ENDPOINT_NOT_FOUND`, which reads like a missing forwarding endpoint rather than a missing route.

## Jobs and gating

Each suite holds one `acceptance-test-<suite>` lock until both test jobs finish; queued arrivals never cancel a running sweep. `sweep` runs first, then `test` and `nonblocking` in parallel, each depending only on `sweep`. Nothing else sweeps.

Before checkout and each resource command, QA re-verifies the PR is open at the requested revision: started commands finish their cleanup, outdated queued ones fail before touching resources.

`PR validation` aggregates hosted checks and the matrix with `alls-green`, permitting the acceptance skip only for ordinary PRs. `Release acceptance` runs only for a release candidate and permits no skips.

## Non-blocking acceptance

Scenarios that do not yet gate merges; maintainers promote suitable cases into required coverage. `make testacc-nonblocking` sets the `nonblocking` build tag, selects `TestNonBlockingAcceptance`, and writes Go JSON for a step summary. Test bodies stay in their original files as lowercase helpers; only the runner is tagged.

The job is `continue-on-error: true` and posts its real outcome through the Checks API as `Non-blocking acceptance (<suite>)` — keep that informational in branch protection. Its failures gate nothing, and publication proceeds because `Release` waits with `gh run watch --exit-status`. Blocking CI still compiles and lints the tagged code. After changing any of this, follow the [throwaway-PR steps](../../ACCEPTANCE_TESTING.md#confirm-behavior-on-a-throwaway-pr).

## Publication

Release Please manages release PRs only — it creates no tags or releases. On merge, `Release` verifies the PR's identity, waits for the candidate's `Tests` run, requires its `Release acceptance` job to have succeeded, and checks that the merged tree matches the tested head. Missing, failed, skipped, canceled or stale validation blocks publication; absent required-check settings, it cannot prevent an early merge. Keep the release PR open until `Release acceptance` passes so queued phases clear their freshness guards.

Publication reads the version and notes from that exact merge commit and refuses to move an existing tag. GoReleaser builds and signs a draft, the workflow publishes it and marks the PR tagged only on success, then refreshes release-please. Retrying a completed release changes nothing.

To recover an interrupted publication, run `Release` manually on `main` with the merged PR number; it uses the same acceptance gate. It cannot repair phases that failed because the PR was already closed — that needs a fresh candidate: in a repair PR revert the blocked release commit's manifest and changelog while keeping the provider changes, then after merging remove `autorelease: pending` from the blocked PR and run `Release Please` on `main`. Releases predating the acceptance check cannot use this path.

Run the secret-free workflow fixtures locally with:

```sh
node --test .github/acceptance-tests/*.test.mjs .github/release/*.test.mjs
```

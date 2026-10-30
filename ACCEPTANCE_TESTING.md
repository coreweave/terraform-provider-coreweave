# Acceptance testing

Use this guide to run quality assurance (QA) acceptance tests, clean up test resources, and maintain required and non-blocking acceptance coverage. The [release acceptance guide](.github/acceptance-tests/README.md) explains release selection and publication.

## Running acceptance tests

Install the Go and Terraform versions from [.config/mise.toml](.config/mise.toml). Configure `COREWEAVE_API_ENDPOINT` and `COREWEAVE_API_TOKEN` for the designated QA organization through your approved credential setup. Continuous integration (CI) obtains these from the `QA` environment. CoreWeave AI Object Storage uses its own QA endpoint and token. Don't rely on the unit-test endpoint defaults for a live run.

Run one suite or a space-separated list:

```sh
make testacc SUITES=cks TEST_ACC_PARALLEL=3 TEST_ACC_TIMEOUT=60m
make testacc SUITES='caller_identity networking'
```

`make testacc` sets `TF_ACC=1`. Non-blocking acceptance runners are excluded by their build tag. Without `SUITES`, it runs `caller_identity`, `cks`, `networking`, `object_storage`, `inference`, and `container_registry`, in that order. Local commands don't acquire the GitHub suite lock.

The default timeout is `45m` per package. Set `TEST_ACC_TIMEOUT` to override it. Set `TEST_ACC_PARALLEL` to control Go's `-parallel` limit. When `TEST_ACC_PARALLEL` is unset, Go uses its default parallelism.

CI gives required and non-blocking CoreWeave Kubernetes Service (CKS) tests a `60m` timeout. Required CKS acceptance uses parallelism `3`; non-blocking CKS acceptance uses `1` to limit concurrent cluster load. Other suites use the Makefile defaults. The [HotLoad acceptance instructions](README.md#hotload-acceptance-tests) describe the separate HotLoad opt-in.

Container registry also requires `COREWEAVE_CONTAINER_REGISTRY_TEST_ZONE` and `COREWEAVE_CONTAINER_REGISTRY_TEST_PREFIX`. CI uses `US-LAB-01A` and defaults to `q-tfacc-cr-`. The prefix must end in `tfacc-cr-` and meet the validation in its sweeper. Inference CI sets `INFR_ZONE=RNO2A` and `INFR_INSTANCE_ID=gd-1xgh200`.

Run ordinary unit tests with `TF_ACC` unset:

```sh
unset TF_ACC
make test
```

`make test`, `go test ./...`, and ordinary acceptance commands need no exclusion filter. They don't enable the `nonblocking` build tag. For example:

```sh
TF_ACC=1 go test ./coreweave/cks -parallel=3 -timeout=60m
```

## Naming and cleanup

Use the suite's existing name generator and a random suffix. The general acceptance prefix is `test-acc-`. CKS cluster and VPC names must be at most 30 characters, including the prefix and suffix. Object storage and container registry use different prefixes required by their existing tests.

| Resource | Sweeper prefix |
| --- | --- |
| CKS cluster | `test-acc-` |
| CKS VPC | `test-acc-cks-vpc-` |
| Networking VPC | `test-acc-vpc-` |
| Inference | `test-acc-inf-` |
| Object storage | `tf-acc-objs-` |
| Container registry | Configured prefix, `q-tfacc-cr-` in CI by default |

Caller identity creates no resources and registers no sweeper. CKS, networking, caller identity, and container registry share `US-LAB-01A` under separate suite locks. Object storage uses `US-EAST-04A`. Inference uses `RNO2A`.

The repository prefix test compares prefixes only among sweepers that delete the same resource type. CKS clusters can use `test-acc-` because the VPC and inference sweepers delete different resource types. CKS and networking VPC sweepers use distinct, non-overlapping prefixes. No same-type overlap exceptions are needed. Registry prefix overrides still need review for isolation.

Acceptance harness cleanup runs for each test. Sweepers remove leftovers from earlier runs. Preview a sweep before running it:

```sh
make testacc-sweep SUITES=cks SWEEP_DRY_RUN=true
make testacc-sweep SUITES=cks
```

`TEST_ACC_SWEEP_ZONE` defaults to `US-LAB-01A`. Use the suite's zone for other suites. Sweep parallelism defaults to `4` and the command timeout to `45m`. Override them with `TEST_ACC_SWEEP_PARALLEL` and `TEST_ACC_SWEEP_TIMEOUT`. CI gives the inference sweep `105m`.

Sweepers skip resources younger than `TEST_ACC_SWEEP_MIN_AGE`, which defaults to `2h`. Set a positive Go duration, such as `3h`, to increase it. The default exceeds the current suite timeouts. Sweepers log every age-based skip. When an API supports a timestamp, sweepers also skip resources with a missing or invalid timestamp. Dry runs apply the same guard.

The guard covers clusters, VPCs, registry namespaces, buckets, inference deployments, capacity claims, and gateways. Object-storage organization access policies expose no creation timestamp. Their sweeper retains its existing prefix-only behavior. It can't protect a local policy test by age.

Local and manual runs share QA resources with CI but have no CI lock. The age guard protects recent resources, not all active runs. A long local run can outlive it.

Coordinate shared runs and choose a minimum age longer than the expected run duration. With the `2h` guard, resources left by a failed run remain until they are at least two hours old and a later sweep removes them. QA quota can remain in use for that long, or longer if no sweep runs. Don't lower the threshold to clean up a resource that another run may still use.

## Non-blocking acceptance

Non-blocking acceptance tests exercise scenarios that do not currently gate merges or releases. They run separately and report their actual results. Maintainers review these results and move suitable cases into the required acceptance suite.

Keep each test's body in its original `_test.go` file. Rename its function with a lowercase `test` prefix so Go doesn't discover it as a standalone test. Add `t.Helper()` and a comment explaining how to restore required coverage:

```go
// Runs in the non-blocking acceptance suite.
// To restore required coverage, see ACCEPTANCE_TESTING.md#non-blocking-acceptance.
func testClusterLifecycle(t *testing.T) {
  t.Helper()
  // Keep the existing test configuration and assertions here.
}
```

Each package with non-blocking acceptance tests defines exactly one `TestNonBlockingAcceptance` in `nonblocking_acceptance_test.go`. Put `//go:build nonblocking` at the top, followed by a blank line. Build tags apply to whole files, so only the runner needs this tag. Give every subtest a descriptive string-literal name in lowercase snake_case, matching `^[a-z][a-z0-9_]*$`. Keep an inline callback that records the marker and calls the original test body:

```go
//go:build nonblocking

package cks_test

import (
  "testing"

  "github.com/coreweave/terraform-provider-coreweave/internal/testutil"
)

func TestNonBlockingAcceptance(t *testing.T) {
  t.Run("cluster_lifecycle", func(t *testing.T) {
    testutil.NonBlockingAcceptance(t)
    testClusterLifecycle(t)
  })
}
```

These examples show the layout. Supply the configuration and assertions for the scenario.

The helper calls `t.Helper()`, records `t.Attr("acceptance_mode", "nonblocking")`, and logs the execution mode. It doesn't skip the test or hide a failure. Keep the assertions for the desired behavior.

Don't use `/` in subtest names. Go splits `-run` patterns on `/`. Don't call `t.Parallel()` in a subtest that calls `resource.ParallelTest()`. That helper already calls `t.Parallel()`, and calling it twice panics.

`TestAcceptanceConventions()` parses every `_test.go` file with `go/parser`. It requires the runner to use the `nonblocking` build tag. It rejects any other top-level test name containing `NonBlocking`, nonliteral or malformed subtest names, and subtests without their own marker call. Source-fixture tests cover the checker.

Normal tests still compile the untagged test bodies. The lint configuration enables the `nonblocking` tag so lint also sees the runner and its calls to those bodies. Blocking CI compiles the tagged tests without running them, with `TF_ACC` unset:

```sh
unset TF_ACC
go test -tags=nonblocking -run '^$' ./...
```

Run the non-blocking group separately:

```sh
make testacc-nonblocking SUITES=cks TEST_ACC_PARALLEL=1 TEST_ACC_TIMEOUT=60m
```

The target sets `TF_ACC=1`, adds `-tags=nonblocking`, selects `^TestNonBlockingAcceptance$`, and writes Go JSON to `.test-results/nonblocking-acceptance/[SUITE].json`, where `[SUITE]` is the suite name. It disables Go result caching with `-count=1`. To change the output directory, override `NONBLOCKING_RESULTS_DIR`. It retains failure status while running every selected suite. A package without the group reports no tests to run and passes. Unit-level cases in a suite package use the same group and job.

The CI summary identifies each marked case by package and subtest name and shows its result. A pass says: `Passing: review for inclusion in required acceptance tests.` After reviewing the results, restore the body's uppercase `Test` prefix and remove its runner registration and temporary comment. Use a normal test name without `NonBlocking`. The body stays in its original file with its assertions intact. Missing terminal results are `UNKNOWN`, not a pass.

## CI behavior

The reusable workflow holds one `acceptance-test-<suite>` lock for the whole run, with `queue: max` and `cancel-in-progress: false`:

1. `sweep` runs first.
2. `test` and `nonblocking` each depend only on `sweep` and can run in parallel.
3. No other job sweeps. The next run's sweep waits for both test jobs to finish.

Before checkout and immediately before its resource command, each job checks that the pull request (PR) is open at the requested head. A stale queued revision fails before using QA resources.

Normal tests and sweep failures feed the blocking `acceptance-tests` result. `PR validation` aggregates that result and hosted checks through `alls-green`. `Release acceptance` depends on normal validation and the acceptance caller. It has no direct dependency on the non-blocking job.

The non-blocking job has job-level `continue-on-error: true`. It also posts a separate Checks API result named `Non-blocking acceptance (<suite>)` from the real test-command outcome. A failure may show both the `Non-blocking acceptance runner (<suite>)` job and the `Non-blocking acceptance (<suite>)` check. Read the check for the reported result. It remains visible even if Actions hides a tolerated job failure. The caller and the non-blocking job grant `checks: write`. Keep the named check informational in branch protection.

A failed (red) non-blocking acceptance check doesn't fail `acceptance-tests`, `PR validation`, `Release acceptance`, or the overall `Tests` workflow. This keeps the test results visible without blocking publication. Local fixtures check the configuration and reporting code. Use the procedure below to verify hosted behavior after workflow changes.

### Confirm behavior on a throwaway PR

Use a disposable branch and PR for this verification. Don't use real resource commands.

1. Temporarily select one suite for the throwaway PR. Replace sweep and test commands with secret-free stubs, preserving the reusable call, freshness guards, dependencies, and job-level tolerance.
2. Make sweep and normal tests succeed. Make the non-blocking stub emit representative Go `attr` and `fail` JSON, then exit nonzero.
3. Confirm that `Non-blocking acceptance (<suite>)` is failed (red) and the summary lists the scenario as `FAIL`. Confirm that the caller `acceptance-tests`, `PR validation`, and `Release acceptance` all succeed.
4. Replace `[RUN-ID]` with the workflow run ID. Confirm that the overall `Tests` workflow conclusion is successful and `gh run watch [RUN-ID] --exit-status` exits zero. Publication uses that command before checking `Release acceptance`.
5. Repeat with a passing non-blocking test. Confirm a passing (green) check and the reminder to review the case for required acceptance coverage. Repeat with a failing normal test and a failing sweep. Both must fail the blocking gates.
6. Queue two runs for the same suite. Add sleeps to both test stubs. Confirm that those jobs overlap within one run and the second sweep starts only after both earlier test jobs finish.
7. Save the run and check results for review. Close the throwaway PR and discard its temporary changes.

## Pitfalls we have hit

Choose plan-check hooks carefully. The test harness affects when checks run.

### Plan-only steps skip pre-apply checks

The Terraform testing harness skips `ConfigPlanChecks.PreApply` for `PlanOnly` steps. Use `PostApplyPreRefresh` for a plan-only assertion. Despite its name, that hook checks the plan in a plan-only step. See the node-port shrink case in `TestClusterResource`.

# Acceptance testing

Use this guide to run quality assurance (QA) acceptance tests, clean up test resources, and keep known backend regressions visible. The [release acceptance guide](.github/acceptance-tests/README.md) explains release selection and publication.

## Running acceptance tests

Install the Go and Terraform versions from [.config/mise.toml](.config/mise.toml). Configure `COREWEAVE_API_ENDPOINT` and `COREWEAVE_API_TOKEN` for the designated QA organization through your approved credential setup. Continuous integration (CI) obtains these from the `QA` environment. CoreWeave AI Object Storage uses its own QA endpoint and token. Don't rely on the unit-test endpoint defaults for a live run.

Run one suite or a space-separated list:

```sh
make testacc SUITES=cks TEST_ACC_PARALLEL=3 TEST_ACC_TIMEOUT=60m
make testacc SUITES='caller_identity networking'
```

`make testacc` sets `TF_ACC=1`. It excludes `TestKnownRegressions`. Without `SUITES`, it runs `caller_identity`, `cks`, `networking`, `object_storage`, `inference`, and `container_registry`, in that order. Local commands don't acquire the GitHub suite lock.

The default timeout is `45m` per package. Set `TEST_ACC_TIMEOUT` to override it. Set `TEST_ACC_PARALLEL` to control Go's `-parallel` limit. When `TEST_ACC_PARALLEL` is unset, Go uses its default parallelism.

CI gives normal and regression CoreWeave Kubernetes Service (CKS) tests a `60m` timeout. Normal CKS acceptance uses parallelism `3`; CKS regressions use `1` to limit concurrent cluster load. Other suites use the Makefile defaults. The [HotLoad acceptance instructions](README.md#hotload-acceptance-tests) describe the separate HotLoad opt-in.

Container registry also requires `COREWEAVE_CONTAINER_REGISTRY_TEST_ZONE` and `COREWEAVE_CONTAINER_REGISTRY_TEST_PREFIX`. CI uses `US-LAB-01A` and defaults to `q-tfacc-cr-`. The prefix must end in `tfacc-cr-` and meet the validation in its sweeper. Inference CI sets `INFR_ZONE=RNO2A` and `INFR_INSTANCE_ID=gd-1xgh200`.

Run ordinary unit tests with `TF_ACC` unset:

```sh
unset TF_ACC
make test
```

`make test` excludes the regression group, including unit-level regressions. A direct `go test ./...` doesn't exclude that group. For a direct acceptance command, include the selector:

```sh
TF_ACC=1 go test ./coreweave/cks -skip '^TestKnownRegressions$' -parallel=3 -timeout=60m
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

## Known regressions

Put known backend bug tests in exactly one top-level `TestKnownRegressions` function per package. Give every subtest a literal name beginning with its ticket and an underscore, such as `CKSAPI-753_same_name_replace`. Mark each subtest with `testutil.KnownRegression()`.

This layout is illustrative. Supply the resource configuration and assertions for the bug:

```go
func TestKnownRegressions(t *testing.T) {
  t.Run("CKSAPI-753_same_name_replace", func(t *testing.T) {
    testutil.KnownRegression(t, "CKSAPI-753")
    // Run the reproduction and assert the correct behavior here.
  })
}
```

The helper validates tickets against `^[A-Z]+-[0-9]+$`, calls `t.Helper()`, records `t.Attr("known_regression", ticket)`, and logs the ticket. It doesn't skip the test or hide a failure. Keep the assertion for the desired behavior so a broken backend produces a failure.

Don't use `/` in subtest names. Go splits `-run` and `-skip` patterns on `/`. Don't call `t.Parallel()` in a subtest that calls `resource.ParallelTest()`. That helper already calls `t.Parallel()`, and calling it twice panics.

`TestAcceptanceConventions()` parses every `_test.go` file with `go/parser`. It rejects any other top-level test name containing `Regression`, nonliteral or malformed subtest names, and subtests without their own marker call. Source-fixture tests cover the checker.

Run the regression group separately:

```sh
make testacc-regressions SUITES=cks TEST_ACC_PARALLEL=1 TEST_ACC_TIMEOUT=60m
```

The target sets `TF_ACC=1`, selects `^TestKnownRegressions$`, and writes Go JSON to `.test-results/known-regressions/[SUITE].json`, where `[SUITE]` is the suite name. It disables Go result caching with `-count=1`. To change the output directory, override `REGRESSION_RESULTS_DIR`. It retains failure status while running every selected suite. A package without the group reports no tests to run and passes. Unit-level regressions in a suite package use the same group and job.

The CI summary associates each `known_regression` attribute with its package, subtest, ticket, and result. A pass says: `fixed: graduate this test out of TestKnownRegressions`. Confirm the backend fix, move the case into the normal test suite, remove the marker, and use a normal test name without `Regression`. Retain its assertions. Missing terminal results are `UNKNOWN`, not a pass.

## CI behavior

The reusable workflow holds one `acceptance-test-<suite>` lock for the whole run, with `queue: max` and `cancel-in-progress: false`:

1. `sweep` runs first.
2. `test` and `regressions` each depend only on `sweep` and can run in parallel.
3. No other job sweeps. The next run's sweep waits for both test jobs to finish.

Before checkout and immediately before its resource command, each job checks that the pull request (PR) is open at the requested head. A stale queued revision fails before using QA resources.

Normal tests and sweep failures feed the blocking `acceptance-tests` result. `PR validation` aggregates that result and hosted checks through `alls-green`. `Release acceptance` depends on normal validation and the acceptance caller. It has no direct regression-job dependency.

The regression job has job-level `continue-on-error: true`. It also posts a separate Checks API result named `Known regressions (<suite>)` from the real test-command outcome. A failure may show both the `Known regression runner (<suite>)` job and the `Known regressions (<suite>)` check. Read the check for the reported result. It remains visible even if Actions hides a tolerated job failure. The caller and the regression job grant `checks: write`. Keep the named check informational in branch protection.

The intended behavior is that a failed (red) regression check doesn't block either gate or publication. Propagation of `continue-on-error` through the reusable workflow hasn't been confirmed in GitHub. Local fixtures verify the configuration and reporting code, not that hosted behavior.

### Confirm behavior on a throwaway PR

Use a disposable branch and PR for this verification. Don't use real resource commands.

1. Temporarily select one suite for the throwaway PR. Replace sweep and test commands with secret-free stubs, preserving the reusable call, freshness guards, dependencies, and job-level tolerance.
2. Make sweep and normal tests succeed. Make the regression stub emit representative Go `attr` and `fail` JSON, then exit nonzero.
3. Confirm that `Known regressions (<suite>)` is failed (red) and the summary lists the ticket as `FAIL`. Confirm that the caller `acceptance-tests`, `PR validation`, and `Release acceptance` all succeed.
4. Replace `[RUN-ID]` with the workflow run ID. Confirm that the overall `Tests` workflow conclusion is successful and `gh run watch [RUN-ID] --exit-status` exits zero. Publication uses that command before checking `Release acceptance`.
5. Repeat with a passing regression. Confirm a passing (green) check and the graduation message. Repeat with a failing normal test and a failing sweep. Both must fail the blocking gates.
6. Queue two runs for the same suite. Add sleeps to both test stubs. Confirm that those jobs overlap within one run and the second sweep starts only after both earlier test jobs finish.
7. Save the run and check results for review. Close the throwaway PR and discard its temporary changes.

## Pitfalls we have hit

Choose plan-check hooks and replacement scenarios carefully. These cases explain where the test harness and backend behavior affect coverage.

### Plan-only steps skip pre-apply checks

The Terraform testing harness skips `ConfigPlanChecks.PreApply` for `PlanOnly` steps. Use `PostApplyPreRefresh` for a plan-only assertion. Despite its name, that hook checks the plan in a plan-only step. See the node-port shrink case in `TestClusterResource`.

### Same-name cluster replacement races with cleanup

CKSAPI-753 tracks a backend race that breaks cluster replacement when the new cluster reuses the old name. CLDAPI-1174's workaround renames the cluster on replacement in `TestClusterResource`. This preserves normal replacement coverage while avoiding that race. That workaround is separate from this mechanism. Until that workaround is merged, `TestClusterResource` still uses the same name.

Moving its same-name reproduction into `TestKnownRegressions` is a follow-up. Keep that case failing until CKSAPI-753 is fixed, then graduate it into normal coverage.

# Acceptance sweeper implementer guide

Acceptance sweepers remove test resources that survived an interrupted or failed acceptance run. They are a recovery mechanism for narrowly identifiable test artifacts, not a general garbage collector. A safe sweeper makes ownership obvious, selects conservatively, deletes by the immutable identity returned by the list operation, and waits for the service-specific deletion contract to complete.

The mental model is: constrain blocker order across resource types, select resources locally, and bound work within one resource type. The Terraform testing framework supplies the first layer, `testutil.Sweep` supplies the second, and a service adapter supplies the third.

## Upstream references and local authority

Start with HashiCorp's [official sweeper guide](https://developer.hashicorp.com/terraform/plugin/testing/acceptance-tests/sweepers). It documents the purpose of sweepers, `resource.TestMain`, unique registration keys, acceptance-test name prefixes, dependencies, and the warning that a sweep destroys real infrastructure. The [`terraform-plugin-testing` resource API](https://pkg.go.dev/github.com/hashicorp/terraform-plugin-testing@v1.11.0/helper/resource#TestMain) is the API reference for `TestMain`, `AddTestSweepers`, `Sweeper`, and the native `-sweep`, `-sweep-run`, and `-sweep-allow-failures` flags. This repository pins that API version in [`go.mod`](../../go.mod#L30).

HashiCorp's [acceptance-test requirements and recommendations](https://developer.hashicorp.com/terraform/plugin/testing/acceptance-tests#requirements-and-recommendations) are the operational baseline: acceptance tests use real infrastructure, may consume quota or incur cost, and should run in a separate account or namespace. CoreWeave-specific controls and defaults are defined by the repository sources linked in [Operating sweepers](#operating-sweepers); they are additions to the upstream runner, not HashiCorp defaults.

## The three layers

### 1. Terraform testing registry and dependency graph

Each package registers [`resource.Sweeper`](https://pkg.go.dev/github.com/hashicorp/terraform-plugin-testing@v1.11.0/helper/resource#Sweeper) values with [`resource.AddTestSweepers`](https://pkg.go.dev/github.com/hashicorp/terraform-plugin-testing@v1.11.0/helper/resource#AddTestSweepers). The registration key is what `-sweep-run` selects, and `Dependencies` names other registration keys that must run first. The framework resolves dependencies recursively, includes them when a dependent sweeper is selected, and runs each selected sweeper at most once per zone in a sweep invocation. Independent sweepers run sequentially in an unspecified order; registration order must not be used to encode safety or deletion ordering. The dependency graph must be acyclic: the framework recursively traverses dependencies without cycle detection.

Dependencies encode deletion blockers, not conceptual relationships. The canonical example is CKS: `coreweave_cks_vpc` depends on `coreweave_cks_cluster`, so leaked clusters are swept before the VPCs that may still be attached to them.

Keep naming intentional and consistent:

- The `resource.AddTestSweepers` registration key and `resource.Sweeper.Name` must be identical.
- Dependency entries must use that same registered key.
- `testutil.SweepConfig.ResourceType` should be the actual Terraform resource type used in logs and errors.

Usually all three values are identical. A suite-owned sweeper for an underlying resource may need a distinct registry key to avoid colliding with another suite: CKS registers its VPC cleanup as `coreweave_cks_vpc`, while the adapter reports the underlying `coreweave_networking_vpc` resource type. Preserve and test such distinctions explicitly.

`-sweep-run` accepts a comma-separated list of case-insensitive substring filters over Terraform testing registry keys. It does not guarantee exact matching, so use one full key to narrow the selection and inspect the dry-run output. A filter that matches no key is a successful no-op, not an error. Avoid empty tokens: leading or trailing commas and doubled commas introduce an empty substring, which matches every registered key. Selected sweepers automatically include their recursively declared dependencies.

### 2. Generic `testutil.Sweep` runner

`testutil.Sweep` owns behavior that should be identical across services:

- It validates `ResourceType`, positive parallelism, and all four callbacks before making a list request.
- It calls `List` exactly once.
- It requires an explicit `Match` callback; selection is never inferred from `Name` or `ResourceType`.
- It captures each selected resource's display name and stably sorts selected resources by that name. Dry-run logs are deterministic, sequential delete attempts are deterministic when `Parallelism` is 1, and aggregated delete errors are reported in sorted selection order.
- In dry-run mode it logs every selected resource and performs no deletes.
- In delete mode it logs each attempt and runs at most `Parallelism` deletes concurrently. `SweepRuntimeFromEnv` defaults this bound to 4.
- It attempts every selected resource when the context remains active, even when earlier deletes fail, and joins deletion errors in deterministic selected-resource order.
- Dispatch stops once the dispatcher observes cancellation, but cancellation can race a ready handoff. Every job successfully handed to a worker invokes `Delete` exactly once with the shared context, even when that context is already canceled. `Sweep` waits for those callbacks, preserves their non-cancellation failures, and returns the context error; callbacks must return promptly because `Sweep` cannot forcibly stop one that ignores its context.

These properties make sweeps reviewable before execution, faster without unbounded API pressure, and diagnostically useful when several leaked resources fail independently. With concurrent deletion, callback start order and delete log order are scheduler-dependent; only selection and aggregate error order are deterministic.

### 3. Service adapter

The adapter builds `SweepConfig[T]` and owns everything specific to the API:

- Listing the resource type and wrapping list errors with service context.
- Extracting a stable display name.
- Proving that a resource belongs to acceptance tests with a narrow matcher.
- Deleting by the immutable ID from the listed object whenever the API exposes one.
- Treating NotFound as successful cleanup.
- Handling transitional states that temporarily reject deletion.
- Polling until deletion is confirmed when the service deletes asynchronously.
- Choosing and enforcing service-specific operation and polling timeouts.

The generic runner deliberately does not provide service idempotency, state-transition handling, completion polling, retry policy, or timeout selection. Putting those behaviors in `Sweep` would erase API differences and make a generic helper appear safer than its adapters actually are.

## Selection safety

A prefix is an ownership boundary. HashiCorp's [sweeper guidance](https://developer.hashicorp.com/terraform/plugin/testing/acceptance-tests/sweepers) recommends a recognizable acceptance-test prefix; in this provider, make that prefix exclusive to the package's acceptance resources and never broaden a matcher merely to catch an older naming pattern. If two suites create the same underlying resource type, give each suite a distinct prefix so each sweeper owns only its own leaks. CKS VPCs, for example, use their CKS-specific VPC prefix rather than the networking suite's general acceptance prefix.

Combine every cheap discriminator supplied by the API. Most zone-scoped adapters should require both the exact acceptance prefix and the requested zone. Org-scoped resources may have no zone field, but their prefix still must be exclusive.

The Terraform testing framework calls the registered callback argument a region; this provider passes the availability zone from `TEST_ACC_SWEEP_ZONE` through that parameter. This guide calls the operational value a zone except when referring to framework APIs or existing helper names.

At the start of a registered sweeper callback, trim and reject an empty or whitespace-only sweep zone before creating contexts, applying environment defaults, or constructing clients; retain constructor validation for direct callers. Require this operational gate even when an org-scoped adapter does not use the zone for matching. Normalize prefixes with `strings.TrimSpace` before rejecting empty values and capturing them in matchers, so padded configuration uses the intended prefix instead of silently mismatching; `vpcsweeper.New` demonstrates this selector prevalidation.

For resources whose API exposes multiple zones, derive selection from the authoritative service contract: handle zone membership and any defined meaning for an empty zone list, and either make repeated evaluation across requested zones idempotent or assign and test one deterministic owning zone. Do not compare a repeated zone field as though it were singular.

Use `Name` only for matching, logs, and error context. After listing, retain and use the immutable resource ID for delete, get, and poll requests. Names can be reused or changed.

A dry run is advisory, not an approval of immutable targets. It logs names, not IDs, and a later real invocation performs a new list; resources can appear, disappear, or be replaced between the two runs. Quiesce acceptance resource creation, inspect the dry-run names, recheck the effective target configuration, and run the real sweep promptly. The real run's narrow `Match` is the final selection guard, but it does not eliminate this time-of-check/time-of-use window.

Deletion should be idempotent from the sweeper's perspective. A delete or follow-up get that returns the service's NotFound condition means the desired end state already exists and should succeed. Other errors need useful operation and resource context.

For asynchronously deleted resources, return only after the service confirms absence. If a service refuses deletion during create, update, upgrade, or delete transitions, poll to a stable state using the listed ID before retrying deletion. These state rules belong in that service's adapter because status enums, retryable conditions, and API guarantees differ.

## Ordering, concurrency, and timeout budgets

`Dependencies` creates sequential gates between resource types. A dependent type starts only after its blockers have run. `testutil.Sweep` concurrency applies only among matched resources within the one active resource type; it does not make dependency types run concurrently.

Choose each `resource.Sweeper` context timeout from the worst credible cleanup duration for that type. Inside it, use narrower contexts for transition waits, delete calls, and completion polling when those phases need distinct bounds. The `go test -timeout` supplied by `TEST_ACC_SWEEP_TIMEOUT` bounds the entire suite invocation, not one zone. It must cover every unique sweeper selected for each requested zone, plus runner and shutdown headroom; the framework processes both resource types and zones sequentially. The test timeout is a final process guard, not a substitute for adapter timeouts that return actionable errors.

When adding or changing registrations, total the worst-case contexts of all unique sweepers selected per zone, including recursively included dependencies, and keep explicit headroom below `TEST_ACC_SWEEP_TIMEOUT`. The current CKS selection illustrates the calculation: the cluster sweeper has a 30-minute context, the dependent VPC sweeper has a 10-minute context, and the default 45-minute test timeout leaves 5 minutes of headroom.

A full inference sweep selects deployment, capacity-claim, and gateway sweepers, each with a 30-minute context, for a 90-minute worst case before headroom. The reusable acceptance workflow sets `TEST_ACC_SWEEP_TIMEOUT=105m` for inference, providing 15 minutes of headroom, and keeps other suites at 45 minutes. The Make default remains 45 minutes, so a local full inference run must explicitly set a value greater than 90 minutes, such as 105 minutes, or narrow `TEST_ACC_SWEEP_RUN` to the needed full registry key. Selecting only deployment fits its 30-minute bound under the 45-minute default; selecting capacity claim or gateway also includes the deployment dependency and therefore needs more than 60 minutes.

Higher `TEST_ACC_SWEEP_PARALLEL` values can reduce same-type wall time, but they also increase API load and do not shorten sequential dependency gates. Keep the default of 4 unless service limits and tests support another bound.

## Implementation skeleton

Keep the adapter and registration shape recognizable; only client construction and NotFound/polling helpers vary:

```go
const widgetSweeperName = "coreweave_widget"

func normalizeWidgetSweepZone(zone string) (string, error) {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return "", errors.New("widget sweep zone must not be empty")
	}
	return zone, nil
}

func newWidgetSweepConfig(client widgetSweepClient, prefix, zone string) (testutil.SweepConfig[*Widget], error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return testutil.SweepConfig[*Widget]{}, errors.New("widget sweep prefix must not be empty")
	}
	zone, err := normalizeWidgetSweepZone(zone)
	if err != nil {
		return testutil.SweepConfig[*Widget]{}, err
	}

	return testutil.SweepConfig[*Widget]{
		ResourceType: widgetSweeperName,
		List: func(ctx context.Context) ([]*Widget, error) {
			items, err := client.ListWidgets(ctx)
			if err != nil {
				return nil, fmt.Errorf("list widgets: %w", err)
			}
			return items, nil
		},
		Name: func(item *Widget) string { return item.GetName() },
		Match: func(item *Widget) bool {
			return strings.HasPrefix(item.GetName(), prefix) && item.GetZone() == zone
		},
		Delete: func(ctx context.Context, item *Widget) error {
			id := item.GetId()
			if err := client.DeleteWidget(ctx, id); isNotFound(err) {
				return nil
			} else if err != nil {
				return fmt.Errorf("delete widget %q: %w", item.GetName(), err)
			}
			if err := waitForWidgetDelete(ctx, client, id); err != nil {
				return fmt.Errorf("wait for widget %q deletion: %w", item.GetName(), err)
			}
			return nil
		},
	}, nil
}

func newWidgetSweeper() *resource.Sweeper {
	return &resource.Sweeper{
		Name:         widgetSweeperName,
		Dependencies: []string{blockingResourceSweeperName},
		F: func(zone string) error {
			zone, err := normalizeWidgetSweepZone(zone)
			if err != nil {
				return err
			}
			runtime, err := testutil.SweepRuntimeFromEnv()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), widgetSweeperTimeout)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := buildWidgetSweepClient(ctx)
			if err != nil {
				return fmt.Errorf("build widget client: %w", err)
			}
			config, err := newWidgetSweepConfig(client, acceptanceTestPrefix, zone)
			if err != nil {
				return err
			}
			return testutil.Sweep(ctx, runtime, config)
		},
	}
}

func init() {
	resource.AddTestSweepers(widgetSweeperName, newWidgetSweeper())
}
```

In the skeleton above, `Widget`, the client calls, NotFound detection, transition handling, and completion polling are placeholders for the service's authoritative API behavior. Do not omit them by reducing the adapter to name-based deletion.

Validate the selector and sweep runtime before creating contexts, mutating environment defaults, or building clients so malformed operator input fails without avoidable setup or side effects.

## Operating sweepers

A bare `make testacc-sweep` is destructive because `SWEEP_DRY_RUN` defaults to `false`. Always set it explicitly to `true` for the preflight run.

Before any invocation, verify the effective API and, where applicable, object-storage endpoints; authenticated account or organization; token permissions; suite; registry filter; and zone. Do this through scoped identity/configuration checks without printing tokens or other credentials.

Use a dedicated acceptance-test account or namespace as [recommended by HashiCorp](https://developer.hashicorp.com/terraform/plugin/testing/acceptance-tests#requirements-and-recommendations). The endpoint, identity, and selector checks above are provider-specific safeguards in addition to that isolation boundary.

Start with a dry run and the narrowest suite, full registry key, and zone:

```sh
make testacc-sweep \
  SUITES=cks \
  TEST_ACC_SWEEP_RUN=coreweave_cks_vpc \
  TEST_ACC_SWEEP_ZONE=US-LAB-01A \
  SWEEP_DRY_RUN=true
```

Quiesce acceptance creation, review every selected name, recheck the endpoint/account/scope/suite/zone preflight, then repeat promptly with `SWEEP_DRY_RUN=false` to delete. Remember that the real run re-lists resources and can select a different set. Because the selected CKS VPC sweeper declares the cluster dependency, that invocation includes the CKS cluster sweeper first.

The provider defaults below are defined in the [`GNUmakefile`](../../GNUmakefile#L14-L19). [`SweepRuntimeFromEnv`](sweepers.go#L14-L49) defines the dry-run and worker-parallelism behavior, and the [reusable acceptance workflow](../../.github/workflows/acceptance-test.yaml#L21-L25) supplies suite-specific timeout and zone overrides in CI. The `-sweep-run` and `-sweep-allow-failures` semantics come from the upstream [`resource.TestMain`](https://pkg.go.dev/github.com/hashicorp/terraform-plugin-testing@v1.11.0/helper/resource#TestMain) runner.

The Make controls are:

| Control | Default | Meaning |
| --- | --- | --- |
| `SUITES` | `caller_identity cks networking object_storage inference` | Space-separated Go package suffixes. The Make target invokes each suite sequentially as `./coreweave/<suite>`. Do not comma-separate this value. |
| `TEST_ACC_SWEEP_RUN` | empty | Optional value passed to `go test -sweep-run`. It is a comma-separated list of case-insensitive substring filters; use one full registry key for the narrowest practical selection. Dependencies are included automatically. Empty runs all registered sweepers, a no-match typo succeeds without running one, and any empty token in a non-empty list matches every key. |
| `TEST_ACC_SWEEP_ALLOW_FAILURES` | `false` | Passed to `-sweep-allow-failures`. `true` lets the framework continue after a failure and can run a dependent sweeper even though its blocker failed; failures are still reported and the suite invocation remains unsuccessful. Keep `false` for dependency-gated cleanup. |
| `TEST_ACC_SWEEP_PARALLEL` | `4` | Maximum concurrent `Delete` callbacks within each `testutil.Sweep` call. Must be a positive integer. |
| `TEST_ACC_SWEEP_TIMEOUT` | `45m` | Local/Make `go test` timeout for each suite invocation. It must cover all unique sweepers selected across every requested zone, including dependencies, plus shutdown headroom. The reusable acceptance workflow passes `105m` for inference and `45m` for every other suite; local full inference runs must explicitly override the 45-minute Make default. |
| `TEST_ACC_SWEEP_ZONE` | `US-LAB-01A` | Value passed to `go test -sweep`. Registered callbacks should trim it and reject empty or whitespace-only values before setup; zone-scoped adapters use the normalized value for matching. |
| `SWEEP_DRY_RUN` | `false` | When true, list, match, sort, and log selected resources without invoking `Delete`. Set this explicitly to `true` for the first operational pass. |

For example, a narrow real run after reviewing the dry run is:

```sh
make testacc-sweep SUITES=cks TEST_ACC_SWEEP_RUN=coreweave_cks_vpc TEST_ACC_SWEEP_ZONE=US-LAB-01A SWEEP_DRY_RUN=false
```

## Implementation checklist

1. Identify the acceptance test's exclusive name prefix and all additional ownership fields, especially zone or location. At the start of the registered callback, trim and reject an empty or whitespace-only sweep zone before setup, and retain required-selector validation in directly callable constructors.
2. Reuse one registry-key constant for `resource.AddTestSweepers`, `Sweeper.Name`, and dependency references. Reuse it for `SweepConfig.ResourceType` too unless a documented suite-owned adapter reports a different underlying Terraform resource type, as the CKS VPC sweeper does.
3. List once and return complete objects containing immutable IDs. Make `Name` stable and side-effect free.
4. Make `Match` explicit and narrow. Add negative cases for production-like names, near-miss prefixes, wrong zones, and absent scope data.
5. Delete and poll by the listed immutable ID. Treat service-specific NotFound responses as success.
6. Implement transitional-state handling and post-delete confirmation required by the actual service contract.
7. Add only real deletion blockers to `Dependencies`; verify every dependency key is registered in the same test binary and the complete graph is acyclic.
8. Set per-phase and per-sweeper timeouts, then verify the sum of all unique sweepers selected per zone fits below `TEST_ACC_SWEEP_TIMEOUT` with headroom.
9. Register the sweeper in the service package and ensure that package's `TestMain` calls `resource.TestMain`.
10. Before live use, verify effective endpoints, authenticated account or organization, token scope, suite, filter, and zone without exposing credentials; quiesce creators and run a narrowly selected dry run.

## Test expectations

Test behavior at the layer that owns it. The `internal/testutil` runner suite proves runtime parsing and validation, list-once behavior, filtering, stable sorting, dry-run behavior, attempt-all deletion, deterministic aggregation and logging, bounded concurrency, and cancellation semantics. Service adapter tests must not pass configurations through `testutil.Sweep` merely to re-prove those generic guarantees.

Adapter tests should stay proportional and cover only adapter-owned policy:

- Selector validation and normalization before setup, plus match positives and ownership near misses for every discriminator.
- Registration keys, `Sweeper.Name`, dependency keys and graph, and any intentional difference in `ResourceType`.
- Preservation of the immutable identity returned by listing for delete, get, and poll operations.
- Genuinely service-specific list error context, NotFound handling, transitional-state behavior, deletion, and completion polling.

Prefer direct tests of the adapter callbacks or closures. Do not add full RPC client fakes, test-only interfaces, options, or injected functions solely to exhaustively exercise concrete method wiring. When such a fake would dominate the implementation, compilation, focused review, and acceptance tests can cover that wiring.

Complex service state machines are an exception: CKS transition handling merits focused fake-backed tests. The shared VPC adapter also merits narrow client tests because it is reusable production test infrastructure. These exceptions do not require every service adapter to copy their test structure.

Use `t.Context()` for callback calls, `t.Helper()` in helpers, and `t.Setenv()` for environment cases where a value should be present.

## Focused validation

Run checks proportionate to the files changed and include race detection where runner or adapter tests exercise concurrency:

```sh
go test -race ./internal/testutil/...
go test -race ./coreweave/<service> -run 'Sweep'
golangci-lint run ./internal/testutil/... ./coreweave/<service>/...
make generate
git diff --check
git status --short
```

`make generate` should leave generated public documentation unchanged for test-only sweeper work. Inspect `git diff --stat` and `git diff` after validation; unexpected generated or unrelated changes are a failure to resolve, not files to include opportunistically.

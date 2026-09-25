# CKS public access draft

This provider-only draft starts from provider commit `de8c5e0ef15df7e9bfe9084c202f1049bc610d62` and consumes the v1beta1 contract from [cks-api PR #1361](https://github.com/coreweave/cks-api/pull/1361), pinned to `df6109d05f18e58c3d1003df4c2e78be05b1e174`. The canonical brainstorm recorded the PR as open and draft when checked on September 24, 2026; that is historical status, not a claim about its current status or deployment. Final product enablement, compatible API/operator integration, and live qualification remain release dependencies. Stored configuration does not establish gateway enforcement.

The selected contract replaces the earlier unreleased `protected_access.public_gateway.access_policy.allowed_cidrs` shape with flat `public_access.allow_cidrs`, and replaces `public_access.enabled` with optional `public_access.mode`. Migrate draft HCL from `enabled = true` to `mode = "TLS"` and from `enabled = false` to `mode = "DISABLED"`. Omit or null `mode` to retain absent selection. These are draft-only breaking changes, with no compatibility alias, shim, or state migration. The former absent-policy unrestricted and enabled deny-all modes no longer exist. Endpoint/CA work remains associated with CLDAPI-281, and CIDR capability with CLDAPI-1043/825; this provider draft does not implement upstream infrastructure.

## Build against the pinned draft protobufs

Prerequisites: Git, Python 3.8 or newer, Go 1.26.6, and a local Git repository containing the pinned cks-api commit. This may be a cks-api checkout or a provider worktree with the upstream objects already fetched. Terraform is needed for local protocol tests; the repository's lint and generation tools are needed for their respective commands. The helper never checks out a branch or downloads an upstream revision.

From the provider root:

```sh
python3 dev/prepare-cks-api-draft.py /path/to/cks-api
export GOWORK="$PWD/modules-dev/go.work"
unset TF_ACC
go test ./coreweave/cks -run '^TestPublicAccess'
make test
make lint
make generate
```

On this machine, `/usr/bin/python3` can be used if the mise shim reports an untrusted checkout. The helper also prints the shell-quoted absolute `GOWORK` export. Keep `GOWORK` set for all builds, tests, linting, and generation. Do not use cloud acceptance or sweep targets for local contract validation.

The helper reads `api/cks/coreweave/cks/v1beta1/clusters.pb.go` and `public_access.pb.go` from the pinned Git objects using `git show`, exporting their bytes unchanged into ignored `modules-dev/cks-pr1361/`. It removes only the prior draft's obsolete `protected_access.pb.go` from that generated package. It writes a local module, a `SOURCE` revision marker, and `modules-dev/go.work`, which includes both the provider and `tools` modules and replaces only the CKS protobuf module. Re-running it refreshes those draft files. The existing Connect stubs remain compatible because RPC methods are unchanged. The upstream checkout, provider `go.mod`/`go.sum`, and tools manifests are not modified. No separate draft clone needs to be changed.

### Published dependency finding

On September 24, 2026, `go list -m -versions` returned 1,166 versions, with newest schema timestamp `20260630171200`. `go mod download -json buf.build/gen/go/coreweave/cks/protocolbuffers/go@latest` resolved to `v1.36.12-20260630171200-77e2aa82efe3.2`. Inspection of that artifact's v1beta1 package found `clusters.pb.go` and `clusters_protoopaque.pb.go`, but no `PublicAccessConfig` or `public_access` contract. Its v1beta2 `NetworkConfig.PublicAccess` boolean is a different API and is not a substitute. This dated finding supports the local pinned workspace strategy; it does not rule out a later publication.

A September 25, 2026 recheck with `GOWORK=off go list -m -json buf.build/gen/go/coreweave/cks/protocolbuffers/go@latest` resolved to the same previously inspected artifact. This remains a dated observation, not a guarantee about future publications.

The checked-in published protobuf pin does not provide the selected contract. Before merge/release, adopt a compatible published BSR dependency, remove the draft helper and this setup document, and rerun validation with `GOWORK=off` against the published artifact. The local override is not a releasable dependency.

## Provider contract

The resource accepts and the data source reports the same flat object:

```hcl
# Fragment within coreweave_cks_cluster; available only in this provider draft.
public_access = {
  mode        = "TLS"
  allow_cidrs = ["203.0.113.0/24", "2001:db8::/32"]
}
```

These are documentation prefixes, not operational client ranges. Replace them with the intended client source networks.

| Configuration | Selected API contract and provider behavior |
| --- | --- |
| Omit or null `public_access` | No direct public-access configuration; typed null in state. Removing a previous object sends an explicit clear. |
| `public_access = {}` | Present disabled configuration, absent mode, empty CIDR set. |
| Omit or null `mode` | Preserve absent mode; interpreted as disabled. |
| `mode = "MODE_UNSPECIFIED"` | Preserve explicit enum zero; interpreted as disabled. |
| `mode = "DISABLED"` | Preserve explicit enum one; disable direct access. Configured CIDRs remain stored. |
| `mode = "TLS"`, CIDRs omitted/null/empty | Invalid. TLS requires 1–100 valid IPv4 or IPv6 prefixes. |
| `mode = "TLS"`, nonempty valid CIDRs | Preserve explicit enum two; request TLS access for matching source ranges. Actual enforcement requires upstream qualification. |
| Any other mode string | Invalid; rejected before an RPC. Values are case-sensitive. |
| Explicit `0.0.0.0/0` and/or `::/0` | Request all sources for the respective address families. These are never supplied automatically. |
| Invalid prefix, null element, or more than 100 set entries | Invalid, including when disabled. |

The object and mode are optional without defaults. `allow_cidrs` is an unordered set. Null, omitted, and empty repeated values converge to an empty set inside a present object; that normalization grants no access and does not satisfy TLS access's nonempty requirement. Valid CIDR spelling is preserved rather than rewritten. Set order is not significant. Requests, resource state, import, and data-source reads preserve object presence and distinguish absent mode from explicit `MODE_UNSPECIFIED`, `DISABLED`, and `TLS`. Refresh detects externally changed access settings.

A shared validation path checks accepted mode strings, TLS/nonempty, the 100-entry maximum, and valid non-null IPv4 or IPv6 prefixes both during configuration validation and immediately before Create/Update. Prefix validation applies even when disabled. Unknown values defer planning checks; all values must become known and valid before an API mutation. No runtime protobuf-validation dependency is introduced.

Create preserves the desired object's presence and optional mode. Updates send the complete desired subtree with the `public_access` parent mask only when access configuration changes. A nil body with that mask clears the object. Keep `allow_cidrs` in HCL to retain CIDRs when disabling; omitting them clears the list. Unrelated, legacy-`public`-only, and version-only updates omit both the public-access body and mask. This follows the selected API's [parent replacement and clearing contract](https://github.com/coreweave/cks-api/blob/df6109d05f18e58c3d1003df4c2e78be05b1e174/internal/public_access_test.go).

Public-access changes and Kubernetes upgrades must be applied separately. Following [upstream update validation](https://github.com/coreweave/cks-api/blob/df6109d05f18e58c3d1003df4c2e78be05b1e174/internal/cluster.go), the provider rejects a combined change in Update before issuing the RPC. Replacement creates may choose a new version and public-access configuration together; the restriction therefore remains in Update, not a plan hook that cannot identify every replacement decision.

## Legacy access and authentication

The legacy `public` attribute remains independently supported and is not deprecated. Its existing default and request behavior are unchanged. It controls ingress supporting CoreWeave-managed authentication. The selected `public_access` contract provides direct TLS access without CoreWeave-managed authentication. Its allowlist does **not** restrict legacy ingress. Neither attribute infers or changes the other; selecting `TLS` does not migrate or disable legacy ingress. Kubernetes authentication and authorization still apply.

The [resource example](../examples/resources/coreweave_cks_cluster/resource.tf) explicitly keeps legacy `public = false` and configures a restricted direct-access request. The [data-source example](../examples/data-sources/coreweave_cks_cluster/data-source.tf) exposes the stored configuration only. Do not treat either example as evidence of deployed gateway support.

## Acceptance-to-test map

The following local tests cover the contract; their presence is not a claim that a particular run passed.

| Acceptance area | Tests in `coreweave/cks` |
| --- | --- |
| Absent/empty object and absent/`MODE_UNSPECIFIED`/`DISABLED`/`TLS` mode | `TestPublicAccessPresenceRoundTrip`, `TestPublicAccessCreateRequest` |
| Restricted/all-source, disable with retained/cleared CIDRs, whole-object removal, read/import | `TestPublicAccessTerraformLifecycle`, `TestPublicAccessUpdateRequests` |
| Accepted mode strings, TLS/nonempty, invalid/null prefixes, 100/101 boundary, disabled validation | `TestPublicAccessApplyValidation`, `TestPublicAccessTerraformCIDRValidation`, `TestPublicAccessTerraformAccepts100CIDRs` |
| Deferred unknowns, known/valid requirements before mutation | `TestPublicAccessRequiresKnownApplyValues`, `TestPublicAccessTerraformUnknownConfiguration`, `TestPublicAccessTerraformRejectsResolvedInvalidConfiguration` |
| Null/omitted/empty normalization and API order stability | `TestPublicAccessEmptyCIDRsPreserveConfiguration`, `TestPublicAccessTerraformEmptyCIDRs`, `TestPublicAccessTerraformLifecycle` |
| Changed-only parent mask, unrelated updates, external drift | `TestPublicAccessUpdateRequests`, `TestPublicAccessTerraformDriftAndUnrelatedUpdate` |
| Upgrade rejection versus replacement creates | `TestPublicAccessTerraformRejectsUpgradeAndPolicyChange`, `TestPublicAccessTerraformReplacementAllowsVersionAndPolicyChange` |
| Independent legacy ingress | `TestPublicAccessTerraformLegacyPublicIndependence` |

## Worktree verification

Local verification on September 25, 2026 used the pinned `df6109d05f18e58c3d1003df4c2e78be05b1e174` enum contract with `TF_ACC` unset:

- `make test` passed, including conversion and real Terraform protocol coverage for mode presence, validation, lifecycle, import, drift, and update isolation. The CKS package completed in 89.588 seconds. Cloud acceptance tests were skipped.
- `make` passed formatting, lint (0 issues), provider build, and documentation generation. `terraform fmt -check -recursive examples` and `tfplugindocs validate` also passed.
- A separately launched provider binary passed 11 `terraform validate -json` cases: absent/empty configuration, null/unspecified/disabled modes, valid TLS dual-stack access, missing TLS CIDRs, invalid mode strings, and invalid CIDRs while disabled. The temporary CLI fixtures were removed. Exported protobufs matched the upstream Git objects byte-for-byte; provider and tools dependency manifests remained unchanged.

Before implementation, the new TLS-mode lifecycle configuration failed against the old boolean schema. The migrated lifecycle test now passes. These local results do not satisfy the live qualification gates below.

## Qualification and release gates

Local conversion and real Terraform protocol tests use a fake Connect service. They exercise provider validation, planning, requests, state, import, drift, and update isolation without creating cloud resources. They cannot establish gateway reachability or enforcement. Existing cluster lifecycle waiting remains unchanged; neither a cluster reaching RUNNING nor an API response containing `public_access` proves gateway readiness.

Before release:

1. Resolve upstream product/enablement decisions and qualify the compatible API/operator contract, including parent replacement, clearing, optional mode presence, all three enum values, and the version-update restriction.
2. Adopt and validate the published BSR artifact without local workspace replacements, then remove the draft setup files. Keep published manifests unchanged until that dependency is intentionally adopted.
3. Run local provider tests, lint, and schema generation against the release dependency, reviewing resource/data-source contract parity and preserving unrelated provider behavior.
4. Separately authorize and perform live qualification of allowed and denied source ranges, explicit IPv4/IPv6 all-source prefixes, TLS authentication and authorization, disabled/unspecified/absent mode transitions, whole-object clearing, and independent legacy ingress. Include externally changed configuration and propagation behavior. Local tests do not satisfy this gate.

The selected PR adds no gateway endpoint, serving CA, or readiness outputs. Existing `api_server_endpoint` behavior remains unchanged; no gateway hostname or CA is derived or invented. Cloudflare, DNS, Spectrum management, upstream deployment, and live acceptance are outside this provider-only draft.

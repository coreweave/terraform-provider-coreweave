# Renovate operations

The Renovate workflow validates repository configuration on GitHub-hosted runners with a read-only token and no App credentials. It uses Renovate 44.51.2, strict repository validation, and public fixtures that check rejection of invalid settings, global-only settings, and required migrations. Validation inspects artifact commands without running dependency updates. It runs from an empty temporary directory so repository `config.js` files cannot supply executable global configuration.

The privileged dependency writer is paused: this workflow has no scheduled update job or App-token minting step. Existing dependency PRs and branches remain available. Manual dispatch only validates `main`; it cannot select a runner or a candidate ref for privileged execution.

## Restoring dependency writes

A repository or organization owner must approve and establish these controls before a separate reviewed change restores the writer:

1. Create a `Renovate` environment with custom deployment policies allowing only the `main` **branch**, with no tag rules. Verify `deployment_branch_policy.custom_branch_policies` is true and the environment's deployment-branch-policy API lists exactly the branch rule `main`. The writer must reference this environment. Do not rely only on a workflow `if` condition to protect credentials from edited PR workflows.
2. Prefer a dedicated GitHub App installed only on `coreweave/terraform-provider-coreweave`. Provision its ID and private key as `RENOVATE_APP_ID` and `RENOVATE_APP_PRIVATE_KEY` environment secrets. If reusing an organization App, the environment must protect the key and token minting must still specify `repositories: terraform-provider-coreweave`.
3. Remove this repository's access to the organization-level `ORG_RENOVATE_CLIENTID` and `ORG_RENOVATE_PRIVATEKEY` secrets without rotating or deleting credentials needed by other repositories. If their visibility is `all`, the organization owner must change the distribution policy to exclude this repository while preserving other consumers. Remove any repository-level copies. Verify the repository's accessible organization-secret and repository-secret APIs no longer list either name. Also check for equivalent App credentials under other names; renaming alone is not isolation.
4. Verify the environment secrets contain the new names through the environment-secret metadata API. Inspect only names, visibility, selected repository IDs, and deployment rules; never retrieve, print, or commit credential values. Confirm both same-repository PR refs and fork PR refs are excluded from the environment by platform policy.
5. Restore a writer only for trusted `main` push, schedule, and dispatch events, with a fixed GitHub-hosted runner, pinned Renovate, a repository-scoped App token, normal logs, and an explicit small environment allowlist. Dispatch must reject non-`main` refs and must not accept arbitrary shell or runner inputs. Candidate PR validation must remain secret-free and must not invoke Renovate's artifact-update pipeline.
6. Before the first mutating run, inventory existing dependency PRs and branches, preserve stale branches, and gate routine group creation with dependency-dashboard approval. Verify the pinned dry-run planner emits no overlapping bot branches or PRs while manual replacements are in progress. Token isolation alone does not make regrouping safe.

Removing credential use from this workflow does not revoke existing organization secrets from other edited workflows. The owner must complete the secret-distribution changes to establish that boundary. Live fork isolation and multi-PR queue behavior require separate platform verification; the validator fixtures do not prove them.

## Local validation

Use Node 24.21.0 from an empty temporary working directory, replacing `REPO` with the absolute checkout path:

```sh
npx --yes --package renovate@44.51.2 -- renovate-config-validator --strict --no-global "$REPO/.github/renovate.json5"
npx --yes --package renovate@44.51.2 -- node "$REPO/.github/renovate/tests/validate.mjs"
```

These checks cover configuration validation, not grouping or branch planning. Dependency scheduling, compatibility groups, acceptance gating, and restoration of the writer require their own reviewed changes.

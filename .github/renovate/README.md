# Renovate operations

Renovate continues to update dependencies daily at 03:00 UTC, after Renovate configuration changes merge to `main`, and through manual dispatch on `main`. The writer uses the existing organization App credentials, with its installation token scoped to `terraform-provider-coreweave`. No new App, environment, secret, or GitHub administrative changes are required.

Configuration PRs run strict, secret-free validation on GitHub-hosted runners. They do not mint an App token or run dependency updates. Manual dispatch on another branch skips both jobs. The writer runs only for main-branch push, schedule, or dispatch events after validation succeeds; it checks out that event's main-branch commit. Active writers are not canceled by a newer run.

Both jobs use the tool versions pinned in the [Renovate workflow](../workflows/renovate.yml). The writer uses normal logs and forwards only `CGO_ENABLED` and the public `GOPROXY` as custom environment variables to artifact commands. Runner-image lookups use a GHCR host rule with the existing job token and `packages: read`. The repository's credential-access policy is unchanged.

## Local validation

Use the Node version pinned in the workflow and an empty temporary working directory. Set `REPO` to the absolute checkout path and `RENOVATE_VERSION` to the workflow's Renovate pin:

```sh
npx --yes --package "renovate@$RENOVATE_VERSION" -- renovate-config-validator --strict --no-global "$REPO/.github/renovate.json5"
npx --yes --package "renovate@$RENOVATE_VERSION" -- node "$REPO/.github/renovate/tests/validate.mjs"
```

The public fixtures check invalid settings, global-only settings, required migrations, and executable global-config isolation. The validator runs from a temporary directory so candidate `config.js` files cannot supply executable global configuration, and it inspects artifact commands without executing them.

Validation uses the supported `renovate-config-validator` CLI from the workflow's pinned package. The fixtures have no separate version pin or dependency on Renovate's internal JavaScript APIs. These checks validate configuration and validator isolation; they do not simulate dependency resolution or grouping behavior.

## Dependency groups

Routine updates group Go patches across both modules and Mise, Terraform plugin libraries including testing and framework helpers, generated Connect/protobuf clients by schema family, AWS SDK with Smithy, remaining Go modules, documentation tools, GitHub Actions, CI containers, and development tools. Mise's runtime and action share a group. Linter updates have their own group so they cannot block Go patches.

Automated Go version updates stay within the minor allowed by the configuration. Raising that cap requires a manual Go/linter upgrade that passes the repository's fast checks. Routine Go and linter releases wait seven days; generated client and runner-image releases retain their existing age exception. Major upgrades stay separate for normal PR review. Security alerts are ungrouped and bypass the routine release-age delay and creation limits.

## PR creation and stale branches

Renovate automatically creates update PRs without Dependency Dashboard approval, subject to a normal budget of three concurrent PRs and two new PRs per hour. Security alerts bypass that budget. No new automerge or GitHub administrative settings are configured.

Stale-branch pruning is enabled. During normal runs, Renovate can close superseded update PRs and remove their obsolete branches.

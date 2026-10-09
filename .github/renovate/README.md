# Renovate operations

Renovate continues to update dependencies daily at 03:00 UTC, after Renovate configuration changes merge to `main`, and through manual dispatch on `main`. The writer uses the existing organization App credentials, with its installation token scoped to `terraform-provider-coreweave`. No new App, environment, secret, or GitHub administrative changes are required.

Configuration PRs run strict, secret-free validation on GitHub-hosted runners. They do not mint an App token or run dependency updates. Manual dispatch on another branch skips both jobs. The writer runs only for main-branch push, schedule, or dispatch events after validation succeeds; it checks out that event's main-branch commit. Active writers are not canceled by a newer run.

Both jobs use Renovate 44.51.2 and Node 24.21.0. The writer uses normal logs and forwards only `CGO_ENABLED` and the public `GOPROXY` as custom environment variables to artifact commands. Runner-image lookups use a GHCR host rule with the existing job token and `packages: read`. The repository's credential-access policy is unchanged.

## Local validation

Use Node 24.21.0 from an empty temporary working directory, replacing `REPO` with the absolute checkout path:

```sh
npx --yes --package renovate@44.51.2 -- renovate-config-validator --strict --no-global "$REPO/.github/renovate.json5"
npx --yes --package renovate@44.51.2 -- node "$REPO/.github/renovate/tests/validate.mjs"
```

The public fixtures check invalid settings, global-only settings, required migrations, and executable global-config isolation. The validator runs from a temporary directory so candidate `config.js` files cannot supply executable global configuration, and it inspects artifact commands without executing them. These checks validate configuration; dependency grouping and branch planning require separate checks.

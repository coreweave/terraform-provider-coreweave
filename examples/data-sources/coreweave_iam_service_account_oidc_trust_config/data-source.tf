data "coreweave_iam_service_account_oidc_trust_config" "github_actions" {
  organization_id    = "a1b2c3"
  service_account_id = "ci-agent"
  id                 = "github-actions"
}

output "trust_config_uid" {
  value = data.coreweave_iam_service_account_oidc_trust_config.github_actions.uid
}

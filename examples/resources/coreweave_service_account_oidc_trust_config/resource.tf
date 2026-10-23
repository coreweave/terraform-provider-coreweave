resource "coreweave_workload_federation_oidc_config" "github" {
  name       = "github-actions"
  issuer_url = "https://token.actions.githubusercontent.com"
  audience   = "https://api.coreweave.com"
}

resource "coreweave_service_account_oidc_trust_config" "main" {
  id                                 = "main-repository"
  organization_id                    = "a1b2c3"
  service_account_id                 = "github-actions"
  workload_federation_oidc_config_id = coreweave_workload_federation_oidc_config.github.id
  description                        = "Trust workflows from the main repository"
  cel                                = <<-EOT
    token.repository == "example/example" &&
    token.ref == "refs/heads/main"
  EOT
}

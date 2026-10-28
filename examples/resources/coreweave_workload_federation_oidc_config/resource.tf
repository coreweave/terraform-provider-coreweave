resource "coreweave_workload_federation_oidc_config" "example" {
  name        = "hcp-terraform"
  description = "HCP Terraform workload identity"
  issuer_url  = "https://app.terraform.io"
  audience    = "https://coreweave.com/iam"
}

# Google is an issuer shared by every Google account, so pin tokens to a
# Google Workspace domain (the `hd` claim) with tenant_id.
resource "coreweave_workload_federation_oidc_config" "google_workspace" {
  name       = "google-workspace"
  issuer_url = "https://accounts.google.com"
  audience   = "https://coreweave.com/iam"
  tenant_id  = "example.com"
}

# GitHub Actions tokens are issued for every repository on GitHub, so pin them
# to a GitHub organization by its numeric ID (the `repository_owner_id` claim)
# with tenant_id.
resource "coreweave_workload_federation_oidc_config" "github_actions" {
  name       = "github-actions"
  issuer_url = "https://token.actions.githubusercontent.com"
  audience   = "https://coreweave.com/iam"
  tenant_id  = "123456789"
}

resource "coreweave_workload_federation_oidc_config" "example" {
  name        = "hcp-terraform"
  description = "HCP Terraform workload identity"
  issuer_url  = "https://app.terraform.io"
  audience    = "coreweave.workload.identity"
}

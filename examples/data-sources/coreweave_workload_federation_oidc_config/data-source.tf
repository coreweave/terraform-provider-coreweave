# Look up a Cloud Console-created OIDC configuration by its stable UID.
data "coreweave_workload_federation_oidc_config" "github_actions" {
  uid = "13db6848-17e8-42b0-8615-4d3fc86bd721"
}

# Look up a configuration by its unique trust identity. The organization is
# determined by the provider's authenticated context.
data "coreweave_workload_federation_oidc_config" "by_trust_identity" {
  issuer_url = "https://token.actions.githubusercontent.com"
  audience   = "coreweave"
}

# Pass the stable UID to a runtime workspace or module without recreating the
# organization-level bootstrap object there.
output "workload_federation_oidc_config_uid" {
  value = data.coreweave_workload_federation_oidc_config.github_actions.uid
}

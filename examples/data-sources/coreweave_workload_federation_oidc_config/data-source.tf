# Look up a Cloud Console-created OIDC configuration by its stable UID.
data "coreweave_workload_federation_oidc_config" "github_actions" {
  uid = "13db6848-17e8-42b0-8615-4d3fc86bd721"
}

# Exact-name lookup is useful when discovering the stable UID. It returns an
# error if no configuration or more than one configuration has this name.
data "coreweave_workload_federation_oidc_config" "by_name" {
  name = "github-actions"
}

# Pass the stable UID to a runtime workspace or module without recreating the
# organization-level bootstrap object there.
output "workload_federation_oidc_config_uid" {
  value = data.coreweave_workload_federation_oidc_config.github_actions.uid
}

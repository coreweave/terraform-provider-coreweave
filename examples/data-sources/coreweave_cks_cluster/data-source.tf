data "coreweave_cks_cluster" "default" {
  id = "1063bce6-6e5b-4b0a-b73a-7e6106b2a77c"
}

# Unreleased draft: this is stored API configuration, not proof of gateway enforcement.
# Direct TLS access does not support CoreWeave-managed authentication.
# Its allowlist does not constrain the independent legacy public ingress.
# mode preserves null, MODE_UNSPECIFIED, DISABLED, or TLS from the API.
output "public_access" {
  value = data.coreweave_cks_cluster.default.public_access
}

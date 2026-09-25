data "coreweave_cks_cluster" "default" {
  id = "1063bce6-6e5b-4b0a-b73a-7e6106b2a77c"
}

output "kube_api_access" {
  value = data.coreweave_cks_cluster.default.kube_api_access
}

# Null until the endpoint and CA are reported.
output "kube_api_endpoint" {
  value = try(data.coreweave_cks_cluster.default.kube_api_access_status.public.endpoint, null)
}

output "kube_api_ca_pem" {
  value = try(base64decode(data.coreweave_cks_cluster.default.kube_api_access_status.ca_data), null)
}

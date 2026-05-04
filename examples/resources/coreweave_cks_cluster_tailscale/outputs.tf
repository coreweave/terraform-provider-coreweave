output "cluster_id" {
  description = "The ID of the CKS cluster."
  value       = coreweave_cks_cluster.default.id
}

output "service_account_oidc_issuer_url" {
  description = "The OIDC issuer URL for the cluster's service accounts."
  value       = coreweave_cks_cluster.default.service_account_oidc_issuer_url
}

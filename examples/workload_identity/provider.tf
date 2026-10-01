variable "service_account_uid" {
  type        = string
  description = "UID of the CoreWeave service account to authenticate as via HCP Terraform workload identity. The external OIDC token is read from TFC_WORKLOAD_IDENTITY_TOKEN."
}

provider "coreweave" {
  authentication = {
    workload_identity = {
      service_account_uid = var.service_account_uid
    }
  }
}

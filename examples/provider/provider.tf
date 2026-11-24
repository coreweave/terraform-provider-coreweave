variable "coreweave_api_token" {
  type        = string
  sensitive   = true
  description = "CoreWeave API token in the form `CW-SECRET-[secret]`. Can also be set via the COREWEAVE_API_TOKEN environment variable."
}

variable "coreweave_org_id" {
  type        = string
  description = "CoreWeave organization targeted by resource operations."
}

variable "service_account_id" {
  type        = string
  description = "UID of the CoreWeave service account used for authentication."
}

variable "service_account_org_id" {
  type        = string
  description = "Organization that owns the authenticating service account."
}

variable "service_account_token_file" {
  type        = string
  description = "Path to the external OIDC assertion. Omit token_file in the provider block to use TFC_WORKLOAD_IDENTITY_TOKEN instead."
}

provider "coreweave" {
  token = var.coreweave_api_token
}

provider "coreweave" {
  alias  = "service_account"
  org_id = var.coreweave_org_id

  authentication = {
    service_account_uid = {
      id         = var.service_account_id
      org_id     = var.service_account_org_id
      token_file = var.service_account_token_file
    }
  }
}

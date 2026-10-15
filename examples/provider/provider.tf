variable "coreweave_api_token" {
  type        = string
  sensitive   = true
  description = "CoreWeave API token in the form `CW-SECRET-[secret]`. Can also be set via the COREWEAVE_API_TOKEN environment variable."
}

provider "coreweave" {
  token = var.coreweave_api_token
}

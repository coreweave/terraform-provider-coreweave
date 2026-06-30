variable "cks_cluster" {
  type = object({
    name = string
    zone = string
  })
  default = {
    name = "tailscale-via-terraform"
    zone = "US-EAST-13A"
  }
}

variable "service_account_oidc_issuer_url" {
  description = "The OIDC issuer URL for the cluster. Populated automatically after the first apply."
  type        = string
  default     = "https://oidc.cks.coreweave.com/id/TODO"
}


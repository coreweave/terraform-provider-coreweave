terraform {
  required_providers {
    coreweave = {
      source = "coreweave/coreweave"
    }
    tailscale = {
      source  = "tailscale/tailscale"
      version = "~> 0.28.0"
    }
  }
}

resource "coreweave_networking_vpc" "default" {
  name = var.cks_cluster.name
  zone = var.cks_cluster.zone
  host_prefixes = [
    {
      name     = "host-v4"
      type     = "PRIMARY"
      prefixes = ["10.16.192.0/18"]
    },
  ]
  vpc_prefixes = [
    {
      name  = "pod-cidr"
      value = "10.0.0.0/13"
    },
    {
      name  = "service-cidr"
      value = "10.16.0.0/22"
    },
    {
      name  = "internal-lb-cidr"
      value = "10.32.4.0/22"
    },
  ]
}

resource "coreweave_cks_cluster" "default" {
  name    = var.cks_cluster.name
  version = "v1.35"
  zone    = var.cks_cluster.zone
  vpc_id  = coreweave_networking_vpc.default.id
  public  = false

  pod_cidr_name          = "pod-cidr"
  service_cidr_name      = "service-cidr"
  internal_lb_cidr_names = ["internal-lb-cidr"]

  tailscale = {
    client_id = tailscale_federated_identity.default.id
  }
}

# After the cluster is created, write the OIDC Issuer URL to terraform.tfvars so subsequent
# plans and applies resolve it without needing a -var flag.
resource "terraform_data" "cluster_tfvars" {
  input = coreweave_cks_cluster.default.service_account_oidc_issuer_url

  provisioner "local-exec" {
    command = "printf 'service_account_oidc_issuer_url = \"%s\"\\n' '${self.input}' > '${path.module}/terraform.tfvars'"
  }
}

resource "tailscale_federated_identity" "default" {
  description = var.cks_cluster.name
  issuer      = var.service_account_oidc_issuer_url
  audience    = var.service_account_oidc_issuer_url
  subject     = "system:serviceaccount:cw-tailscale:tailscale"
  scopes = [
    "auth_keys",
    "services",
  ]
  tags = ["tag:coreweave"]
}

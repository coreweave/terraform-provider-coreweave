resource "coreweave_networking_vpc" "default" {
  name = "default"
  zone = "US-EAST-04A"

  host_prefixes = [
    {
      name     = "primary"
      type     = "PRIMARY"
      prefixes = ["10.16.192.0/18"]
    },
  ]

  vpc_prefixes = [
    {
      type  = "POD"
      name  = "pod cidr"
      value = "10.0.0.0/13"
    },
    {
      type  = "SERVICE"
      name  = "service cidr"
      value = "10.16.0.0/22"
    },
    {
      type  = "LOAD_BALANCER"
      name  = "internal lb cidr"
      value = "10.32.4.0/22"
    },
    {
      name  = "private-access"
      value = "10.48.0.0/24"
      type  = "PRIVATE_ACCESS"
    },
  ]
}

resource "coreweave_cks_cluster" "default" {
  name                   = "default"
  version                = "v1.35"
  zone                   = "US-EAST-04A"
  vpc_id                 = coreweave_networking_vpc.default.id
  public                 = false
  pod_cidr_name          = "pod cidr"
  service_cidr_name      = "service cidr"
  internal_lb_cidr_names = ["internal lb cidr"]
  audit_policy           = filebase64("${path.module}/audit-policy.yaml")
  kubelet                = jsonencode({ maxPods = 256 })

  # Direct public access to the Kubernetes API server, independent of `public`.
  # This feature requires contacting CoreWeave support to enable it for your organization.
  # Once enabled, uncomment this block and replace these documentation prefixes with your client source networks.
  # kube_api_access = {
  #   public = {
  #     allow_cidrs = ["203.0.113.0/24", "2001:db8::/32"]
  #     deny_cidrs  = ["203.0.113.128/25"]
  #   }
  # }

  # Contact your CoreWeave Solutions Architect to enable private control plane access.
  # Configure this at creation; changing it requires cluster replacement.
  # private_access = {
  #   prefix_name_ipv4 = "private-access"
  # }

  oidc = {
    ca              = filebase64("${path.module}/example-ca.crt")
    client_id       = "kbyuFDidLLm280LIwVFiazOqjO3ty8KH"
    groups_claim    = "read-only"
    groups_prefix   = "cw"
    issuer_url      = "https://samples.auth0.com/"
    required_claim  = ""
    signing_algs    = ["SIGNING_ALGORITHM_RS256"]
    username_claim  = "user_id"
    username_prefix = "cw"
  }
  authn_webhook = {
    ca     = filebase64("${path.module}/example-ca.crt")
    server = "https://samples.auth0.com/"
  }
  authz_webhook = {
    ca     = filebase64("${path.module}/example-ca.crt")
    server = "https://samples.auth0.com/"
  }
}

# Null until the endpoint and CA are reported.
output "kube_api_endpoint" {
  value = try(coreweave_cks_cluster.default.kube_api_access_status.public.endpoint, null)
}

output "kube_api_ca_pem" {
  value = try(base64decode(coreweave_cks_cluster.default.kube_api_access_status.ca_data), null)
}

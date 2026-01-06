resource "coreweave_networking_vpc" "default" {
  name        = "default"
  zone        = "US-EAST-04A"
  host_prefix = "10.16.192.0/18"
  vpc_prefixes = [
    {
      name  = "pod cidr"
      value = "10.0.0.0/13"
    },
    {
      name  = "service cidr"
      value = "10.16.0.0/22"
    },
    {
      name  = "internal lb cidr"
      value = "10.32.4.0/22"
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

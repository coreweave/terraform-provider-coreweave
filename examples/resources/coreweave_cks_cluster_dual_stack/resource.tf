resource "coreweave_networking_vpc" "default" {
  name = "default"
  zone = "US-EAST-11A"

  host_prefixes = [
    {
      name     = "host-v4"
      type     = "PRIMARY"
      prefixes = ["10.16.192.0/18"]
    },
    {
      name     = "host-v6"
      type     = "ROUTED"
      prefixes = ["fd00:10:16:100::/56"]

      ipam = {
        prefix_length = 64
      }
    },
  ]

  vpc_prefixes = [
    # Pods
    {
      name  = "pod cidr"
      value = "10.0.0.0/13"
    },
    {
      name  = "pod cidr v6"
      value = "fd12:3456:789a:1000::/56"
    },

    # Services
    {
      name  = "service cidr"
      value = "10.16.0.0/22"
    },
    {
      name  = "service cidr v6"
      value = "fd12:3456:789a:1100::/108"
    },

    # Internal LBs (v4 + v6)
    {
      name  = "internal lb cidr"
      value = "10.32.4.0/22"
    },
    {
      name  = "internal lb cidr v6"
      value = "fd12:3456:789a:2001::/112"
    },
  ]
}

resource "coreweave_cks_cluster" "default" {
  name    = "default"
  version = "v1.35"
  zone    = "US-EAST-11A"
  vpc_id  = coreweave_networking_vpc.default.id
  public  = false

  pod_cidr_name          = "pod cidr"
  service_cidr_name      = "service cidr"
  internal_lb_cidr_names = ["internal lb cidr"]

  # IPv6 (all three must be set together; create-only)
  pod_cidr_name_v6          = "pod cidr v6"
  service_cidr_name_v6      = "service cidr v6"
  internal_lb_cidr_names_v6 = ["internal lb cidr v6"]

  audit_policy = filebase64("${path.module}/audit-policy.yaml")

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


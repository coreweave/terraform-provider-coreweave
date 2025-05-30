---
# generated by https://github.com/hashicorp/terraform-plugin-docs
page_title: "coreweave_cks_cluster Resource - coreweave"
subcategory: ""
description: |-
  CoreWeave Kubernetes Cluster
---

# coreweave_cks_cluster (Resource)

CoreWeave Kubernetes Cluster

## Example Usage

```terraform
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
  version                = "v1.32"
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
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `internal_lb_cidr_names` (Set of String) The names of the vpc prefixes to use as internal load balancer CIDR ranges. Internal load balancers are reachable within the VPC but not accessible from the internet.
The prefixes must exist in the cluster's VPC. This field is append-only.
- `name` (String) The name of the cluster. Must not be longer than 30 characters.
- `pod_cidr_name` (String) The name of the vpc prefix to use as the pod CIDR range. The prefix must exist in the cluster's VPC.
- `service_cidr_name` (String) The name of the vpc prefix to use as the service CIDR range. The prefix must exist in the cluster's VPC.
- `version` (String) The version of Kubernetes to run on the cluster, in minor version format (e.g. 'v1.32'). Patch versions are automatically applied by CKS as they are released.
- `vpc_id` (String) The ID of the VPC in which the cluster is located. Must be a VPC in the same Availability Zone as the cluster.
- `zone` (String) The Availability Zone in which the cluster is located.

### Optional

- `audit_policy` (String) Audit policy for the cluster. Must be provided as a base64-encoded JSON/YAML string.
- `authn_webhook` (Attributes) Authentication webhook configuration for the cluster. (see [below for nested schema](#nestedatt--authn_webhook))
- `authz_webhook` (Attributes) Authorization webhook configuration for the cluster. (see [below for nested schema](#nestedatt--authz_webhook))
- `oidc` (Attributes) OpenID Connect (OIDC) configuration for authentication to the api-server. (see [below for nested schema](#nestedatt--oidc))
- `public` (Boolean) Whether the cluster's api-server is publicly accessible from the internet.

### Read-Only

- `api_server_endpoint` (String) The endpoint for the cluster's api-server.
- `id` (String) The unique identifier of the cluster.
- `status` (String) The current status of the cluster.

<a id="nestedatt--authn_webhook"></a>
### Nested Schema for `authn_webhook`

Required:

- `server` (String) The URL of the webhook server.

Optional:

- `ca` (String) The CA certificate for the webhook server. Must be a base64-encoded PEM-encoded certificate.


<a id="nestedatt--authz_webhook"></a>
### Nested Schema for `authz_webhook`

Required:

- `server` (String) The URL of the webhook server.

Optional:

- `ca` (String) The CA certificate for the webhook server. Must be a base64-encoded PEM-encoded certificate.


<a id="nestedatt--oidc"></a>
### Nested Schema for `oidc`

Required:

- `client_id` (String) The client ID for the OIDC client.
- `issuer_url` (String) The URL of the OIDC issuer.

Optional:

- `ca` (String) The CA certificate for the OIDC issuer. Must be a base64-encoded PEM-encoded certificate.
- `groups_claim` (String) The claim to use as the groups.
- `groups_prefix` (String) The prefix to use for the groups.
- `required_claim` (String) The claim to require for authentication.
- `signing_algs` (Set of String) A list of signing algorithms that the OpenID Connect discovery endpoint uses.
- `username_claim` (String) The claim to use as the username.
- `username_prefix` (String) The prefix to use for the username.

## Import

Import is supported using the following syntax:

```shell
terraform import coreweave_cks_cluster.default {{id}}
```

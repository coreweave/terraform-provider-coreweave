---
# generated by https://github.com/hashicorp/terraform-plugin-docs
page_title: "coreweave_cks_cluster Data Source - coreweave"
subcategory: ""
description: |-
  CoreWeave Kubernetes Cluster
---

# coreweave_cks_cluster (Data Source)

CoreWeave Kubernetes Cluster

## Example Usage

```terraform
data "coreweave_cks_cluster" "default" {
  id = "1063bce6-6e5b-4b0a-b73a-7e6106b2a77c"
}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `id` (String) The ID of the cluster.

### Read-Only

- `api_server_endpoint` (String) The API server endpoint of the cluster.
- `audit_policy` (String) The audit policy of the cluster.
- `authn_webhook` (Attributes) The authentication webhook configuration of the cluster. (see [below for nested schema](#nestedatt--authn_webhook))
- `authz_webhook` (Attributes) The authorization webhook configuration of the cluster. (see [below for nested schema](#nestedatt--authz_webhook))
- `internal_lb_cidr_names` (Set of String) The internal load balancer CIDR names of the cluster.
- `name` (String) The name of the cluster.
- `oidc` (Attributes) The OIDC configuration of the cluster. (see [below for nested schema](#nestedatt--oidc))
- `pod_cidr_name` (String) The pod CIDR name of the cluster.
- `public` (Boolean) Whether the cluster is public.
- `service_cidr_name` (String) The service CIDR name of the cluster.
- `status` (String) The status of the cluster.
- `version` (String) The version of the cluster.
- `vpc_id` (String) The VPC ID of the cluster.
- `zone` (String) The zone of the cluster.

<a id="nestedatt--authn_webhook"></a>
### Nested Schema for `authn_webhook`

Read-Only:

- `ca` (String) The CA certificate of the authentication webhook.
- `server` (String) The server URL of the authentication webhook.


<a id="nestedatt--authz_webhook"></a>
### Nested Schema for `authz_webhook`

Read-Only:

- `ca` (String) The CA certificate of the authorization webhook.
- `server` (String) The server URL of the authorization webhook.


<a id="nestedatt--oidc"></a>
### Nested Schema for `oidc`

Read-Only:

- `client_id` (String) The client ID of the OIDC configuration.
- `issuer_url` (String) The issuer URL of the OIDC configuration.

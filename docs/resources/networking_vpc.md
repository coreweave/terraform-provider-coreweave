---
# generated by https://github.com/hashicorp/terraform-plugin-docs
page_title: "coreweave_networking_vpc Resource - coreweave"
subcategory: ""
description: |-
  CoreWeave VPC
---

# coreweave_networking_vpc (Resource)

CoreWeave VPC



<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `name` (String)
- `zone` (String)

### Optional

- `dns_servers` (Set of String)
- `host_prefixes` (Set of String)
- `pub_import` (Boolean)
- `vpc_prefixes` (Attributes Set) (see [below for nested schema](#nestedatt--vpc_prefixes))

### Read-Only

- `id` (String) The unique identifier of the vpc.

<a id="nestedatt--vpc_prefixes"></a>
### Nested Schema for `vpc_prefixes`

Required:

- `name` (String)
- `value` (String)

Optional:

- `disable_external_propagate` (Boolean)
- `disable_host_bgp_peering` (Boolean)
- `host_dhcp_route` (Boolean)
- `public` (Boolean)

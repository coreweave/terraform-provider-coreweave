variable "owner_org" {
  type = string
}

resource "coreweave_container_registry_namespace" "images" {
  namespace_id                 = "example-images"
  zone                         = "US-LAB-01A"
  initial_access_configuration = {}
}

resource "coreweave_container_registry_access_configuration" "images" {
  namespace = coreweave_container_registry_namespace.images.name
  policy_sets = {
    readers = {
      identity_selector = "COREWEAVE"
      rules = {
        pull = {
          expression = "identity[\"org_id\"] == ${jsonencode(var.owner_org)} && \"read_registry_content\" in identity[\"roles\"] && request[\"type\"] == \"repository\" && request[\"action\"] == \"pull\""
        }
      }
    }
  }
  request_ip_acl = {
    allow_cidrs = ["192.0.2.0/24", "2001:db8::/32"]
    deny_cidrs  = ["192.0.2.128/25"]
  }
}

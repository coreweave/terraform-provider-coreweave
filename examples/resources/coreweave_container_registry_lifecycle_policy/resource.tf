resource "coreweave_container_registry_namespace" "images" {
  namespace_id  = "example-images"
  zone          = "US-LAB-01A"
  force_destroy = false
}

resource "coreweave_container_registry_lifecycle_policy" "images" {
  namespace = coreweave_container_registry_namespace.images.name
  enabled   = true
  rules = {
    expire-ci = {
      regex       = "ci/.*"
      older_than  = "2592000s"
      keep_newest = 10
    }
  }
}

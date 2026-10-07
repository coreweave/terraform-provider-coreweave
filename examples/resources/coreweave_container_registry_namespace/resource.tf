resource "coreweave_container_registry_namespace" "images" {
  namespace_id                 = "example-images"
  zone                         = "US-LAB-01A"
  storage_quota_bytes          = 107374182400
  initial_access_configuration = {}
  force_destroy                = false

  timeouts = {
    create = "30m"
    delete = "30m"
  }
}

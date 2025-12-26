resource "coreweave_object_storage_bucket" "default" {
  name = "my-bucket-with-settings"
  zone = "US-EAST-04A"
}

resource "coreweave_object_storage_bucket_settings" "default" {
  bucket                = coreweave_object_storage_bucket.default.name
  audit_logging_enabled = true
}

resource "coreweave_object_storage_bucket" "default" {
  name = "my-bucket-with-settings"
  zone = "US-EAST-04A"
}

resource "coreweave_object_storage_bucket_settings" "default" {
  bucket                = coreweave_object_storage_bucket.default.name
  audit_logging_enabled = true

  # Archive idle STANDARD objects to STANDARD_IA. Your organization must be
  # entitled to configure archive settings before enabling this.
  archive_enabled                = true
  archive_after_last_access_days = 60
}

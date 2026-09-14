resource "coreweave_object_storage_bucket" "default" {
  name = "my-bucket-with-settings"
  zone = "US-EAST-04A"
}

resource "coreweave_object_storage_bucket_settings" "default" {
  bucket                = coreweave_object_storage_bucket.default.name
  audit_logging_enabled = true

  # Limit STANDARD storage to 1 TiB. Your organization must be entitled to
  # configure per-bucket capacity caps before setting this attribute.
  capacity_cap_bytes = 1099511627776

  # Archive idle STANDARD objects to STANDARD_IA. Your organization must be
  # entitled to configure archive settings before enabling this.
  archive_enabled                = true
  archive_after_last_access_days = 60
}

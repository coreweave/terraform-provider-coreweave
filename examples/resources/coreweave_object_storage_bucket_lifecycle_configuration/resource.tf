resource "coreweave_object_storage_bucket" "default" {
  name = "bucket-lifecycle-example"
  zone = "US-EAST-04A"
}


resource "coreweave_object_storage_bucket_versioning" "default" {
  bucket = coreweave_object_storage_bucket.default.name
  versioning_configuration {
    status = "Enabled"
  }

}

resource "coreweave_object_storage_bucket_lifecycle_configuration" "default" {
  bucket = coreweave_object_storage_bucket.default.name
  ## Ensure bucket versioning is enabled first since we specify noncurrent_version_expiration
  depends_on = [coreweave_object_storage_bucket_versioning.default]

  # Rule 1: Expire old logs and clean up noncurrent versions
  rule {
    id     = "cleanup-logs"
    status = "Enabled"

    # apply to objects under logs/ that have tag env=prod and size > 1MB
    filter {
      and {
        prefix                   = "logs/"
        object_size_greater_than = 1000000
        tags = {
          env = "prod"
        }
      }
    }

    expiration {
      days = 30
    }

    noncurrent_version_expiration {
      noncurrent_days           = 7
      newer_noncurrent_versions = 2
    }
  }

  # Rule 2: Abort abandoned multipart uploads and expire traces after a fixed date
  rule {
    id     = "expire-traces"
    prefix = "traces/"
    status = "Enabled"

    abort_incomplete_multipart_upload {
      days_after_initiation = 5
    }

    expiration {
      date = "2026-01-01T00:00:00Z"
    }
  }
}

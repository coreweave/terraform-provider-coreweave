resource "coreweave_object_storage_bucket" "source" {
  name = "inventory-source-example"
  zone = "US-EAST-04A"
}

resource "coreweave_object_storage_bucket" "destination" {
  name = "inventory-destination-example"
  zone = "US-EAST-04A"
}

# The CoreWeave inventory service writes reports to the destination bucket.
# Grant it only the actions it needs: s3:PutObject to write reports and
# s3:AbortMultipartUpload to clean up uploads left by failed report generation.
resource "coreweave_object_storage_bucket_policy" "destination" {
  bucket = coreweave_object_storage_bucket.destination.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowServiceAccountWriteReportsToDestination"
        Effect = "Allow"
        Principal = {
          CW = "arn:aws:iam::static:role/static/inventory"
        }
        Action = [
          "s3:PutObject",
          "s3:AbortMultipartUpload",
        ]
        Resource = [
          "arn:aws:s3:::${coreweave_object_storage_bucket.destination.name}/*",
        ]
      },
    ]
  })
}

resource "coreweave_object_storage_bucket_inventory" "default" {
  bucket                   = coreweave_object_storage_bucket.source.name
  name                     = "daily-inventory"
  enabled                  = true
  included_object_versions = "All"

  # Optional: omit entirely to include no extra fields. An empty set is invalid.
  optional_fields = ["Size", "LastModifiedDate", "StorageClass", "ETag"]

  # Optional: limit the report to objects under a prefix.
  filter {
    prefix = "logs/"
  }

  schedule {
    frequency = "Daily"
  }

  destination {
    bucket {
      bucket_arn = "arn:aws:s3:::${coreweave_object_storage_bucket.destination.name}"
      format     = "CSV"
      prefix     = "inventory-reports/"
    }
  }

  # Create the destination bucket policy first. PutBucketInventoryConfiguration
  # is rejected if the inventory service cannot PutObject to the destination.
  depends_on = [coreweave_object_storage_bucket_policy.destination]
}

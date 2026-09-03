data "coreweave_caller_identity" "current" {}

resource "coreweave_object_storage_bucket" "source" {
  name = "inventory-source-example"
  zone = "US-EAST-04A"
}

resource "coreweave_object_storage_bucket" "destination" {
  name = "inventory-destination-example"
  zone = "US-EAST-04A"
}

# After a bucket policy exists, CoreWeave AI Object Storage implicitly denies
# any principal or action the policy does not allow. Grant the inventory
# service write access, and grant the Terraform caller access to list, read,
# and manage report objects.
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
      {
        Sid    = "AllowOwnerListDestination"
        Effect = "Allow"
        Principal = {
          CW = "arn:aws:iam::${data.coreweave_caller_identity.current.organization_id}:coreweave/${data.coreweave_caller_identity.current.principal_id}"
        }
        Action = [
          "s3:ListBucket",
        ]
        Resource = [
          "arn:aws:s3:::${coreweave_object_storage_bucket.destination.name}",
        ]
      },
      {
        Sid    = "AllowOwnerManageDestinationObjects"
        Effect = "Allow"
        Principal = {
          CW = "arn:aws:iam::${data.coreweave_caller_identity.current.organization_id}:coreweave/${data.coreweave_caller_identity.current.principal_id}"
        }
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
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

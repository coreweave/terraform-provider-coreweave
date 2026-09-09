# Requires CoreWeave provider v0.22.0 or later for caller_identity.
data "coreweave_caller_identity" "current" {}

locals {
  caller_principal = "arn:aws:iam::${data.coreweave_caller_identity.current.organization_id}:coreweave/${data.coreweave_caller_identity.current.principal_id}"
}

# Replace the example bucket names with globally unique names and configure CoreWeave provider authentication.
resource "coreweave_object_storage_bucket" "source" {
  name = "inventory-source-example"
  zone = "US-EAST-04A"
}

resource "coreweave_object_storage_bucket" "destination" {
  name = "inventory-destination-example"
  zone = "US-EAST-04A"
}

# This resource replaces the entire bucket policy; retain any other required grants.
# Unmatched requests are implicitly denied even if an organization policy allows them.
# Add explicit bucket grants for other report readers. PutBucketPolicy itself uses only organization permissions.
resource "coreweave_object_storage_bucket_policy" "inventory_destination" {
  bucket = coreweave_object_storage_bucket.destination.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowInventoryReports"
        Effect = "Allow"
        Principal = {
          CW = "arn:aws:iam::static:role/static/inventory"
        }
        Action   = ["s3:PutObject", "s3:AbortMultipartUpload"]
        Resource = ["arn:aws:s3:::${coreweave_object_storage_bucket.destination.name}/inventory-reports/*"]
      },
      {
        Sid    = "AllowCallerManageDestination"
        Effect = "Allow"
        Principal = {
          CW = local.caller_principal
        }
        Action = [
          "s3:ListBucket",
          "s3:GetBucketPolicy",
          "s3:DeleteBucketPolicy",
          "s3:GetBucketLocation",
          "s3:GetBucketTagging",
          "s3:DeleteBucket",
        ]
        Resource = ["arn:aws:s3:::${coreweave_object_storage_bucket.destination.name}"]
      },
      {
        Sid    = "AllowCallerManageReports"
        Effect = "Allow"
        Principal = {
          CW = local.caller_principal
        }
        Action   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
        Resource = ["arn:aws:s3:::${coreweave_object_storage_bucket.destination.name}/inventory-reports/*"]
      },
    ]
  })
}

resource "coreweave_object_storage_bucket_inventory" "default" {
  # The service validates destination access when configuring inventory.
  depends_on = [coreweave_object_storage_bucket_policy.inventory_destination]

  bucket                   = coreweave_object_storage_bucket.source.name
  name                     = "daily-inventory"
  enabled                  = true
  included_object_versions = "All"

  # Optional: omit entirely to include no extra fields. An empty set is invalid.
  optional_fields = ["Size", "LastModifiedDate", "LastAccessedDate", "StorageClass", "ETag"]

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
}

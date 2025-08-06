## Example using jsonencode to pass a raw JSON string to the policy attribute

locals {
  bucket_policy = {
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "allow-all"
        Effect = "Allow"
        Principal = {
          "CW" : "*"
        }
        Action   = ["s3:*"]
        resource = ["arn:aws:s3:::${coreweave_object_storage_bucket.raw.name}"]
      },
    ]
  }
}

resource "coreweave_object_storage_bucket" "raw" {
  name = "bucket-policy-raw-example"
  zone = "US-EAST-04A"
}

resource "coreweave_object_storage_bucket_policy" "raw" {
  bucket = coreweave_object_storage_bucket.name
  policy = jsonencode(local.bucket_policy)
}

## Example using the coreweave_object_storage_bucket_policy_document data source

resource "coreweave_object_storage_bucket" "doc" {
  name = "bucket-policy-doc-example"
  zone = "US-EAST-04A"
}

data "coreweave_object_storage_bucket_policy_document" "doc" {
  version = "2012-10-17"
  statement {
    sid      = "allow-all"
    effect   = "Allow"
    action   = ["s3:*"]
    resource = ["arn:aws:s3:::${coreweave_object_storage_bucket.doc.name}"]
    principal = {
      "CW" : ["*"]
    }
  }

  statement {
    sid      = "DenyIfPrefixEquals"
    effect   = "Deny"
    action   = ["s3:ListBucket"]
    resource = ["arn:aws:s3:::${coreweave_object_storage_bucket.doc.name}"]
    principal = {
      "CW" : ["*"]
    }
    condition = {
      "StringNotEquals" : {
        "s3:prefix" : "projects"
      }
    }
  }
}

resource "coreweave_object_storage_bucket_policy" "doc" {
  bucket = coreweave_object_storage_bucket.doc.name
  policy = data.coreweave_object_storage_bucket_policy_document.doc.json
}

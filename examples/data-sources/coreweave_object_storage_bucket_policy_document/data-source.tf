variable "bucket_name" {
  type        = string
  description = "Name of the bucket to allow access to."
}

variable "org_id" {
  type        = string
  description = "CoreWeave organization ID to match in the bucket policy condition."
}

data "coreweave_object_storage_bucket_policy_document" "default" {
  version = "2012-10-17"
  statement {
    sid    = "AllowAllInOrg"
    effect = "Allow"
    action = ["s3:*"]
    resource = [
      "arn:aws:s3:::${var.bucket_name}",
      "arn:aws:s3:::${var.bucket_name}/*",
    ]
    principal = {
      "CW" : ["*"]
    }
    condition = {
      "StringEquals" : {
        "cw:PrincipalOrgID" : var.org_id
      }
    }
  }
}

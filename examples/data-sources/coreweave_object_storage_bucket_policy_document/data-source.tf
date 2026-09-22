terraform {
  required_providers {
    coreweave = {
      source = "coreweave/coreweave"
    }
  }
}

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
    sid    = "AllowUserAndGroup"
    effect = "Allow"
    action = ["s3:*"]
    resource = [
      "arn:aws:s3:::${var.bucket_name}",
      "arn:aws:s3:::${var.bucket_name}/*",
    ]
    principal = {
      "CW"  = ["arn:aws:iam::[ORG-ID]:coreweave/[USER-ID]"]
      "AWS" = ["arn:aws:iam::[ORG-ID]:saml/[SAML-GROUP-ID]"]
    }
    condition = {
      "StringEquals" : {
        "cw:PrincipalOrgID" : var.org_id
      }
    }
  }
}

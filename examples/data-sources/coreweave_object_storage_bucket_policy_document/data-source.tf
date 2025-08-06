data "coreweave_object_storage_bucket_policy_document" "default" {
  version = "2012-10-17"
  statement {
    sid      = "allow-all"
    effect   = "Allow"
    action   = ["s3:*"]
    resource = ["arn:aws:s3:::*"]
    principal = {
      "CW" : ["*"]
    }
  }
}

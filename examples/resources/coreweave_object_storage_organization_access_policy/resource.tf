resource "coreweave_object_storage_organization_access_policy" "test" {
  name = "full-s3-api-access"
  statements = [
    {
      name       = "allow-full-s3-api-access-to-all"
      effect     = "Allow"
      resources  = ["*"]
      principals = ["*"]
      actions = [
        "s3:*",
        "cwobject:*",
      ]
    }
  ]
}

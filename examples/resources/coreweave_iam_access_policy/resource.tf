resource "coreweave_iam_access_policy" "automation" {
  organization_id  = "a1b2c3"
  access_policy_id = "automation-deployers"
  description      = "Permissions used by deployment automation"

  rules = [
    {
      actions = [
        "read_auth",
        "write_auth",
      ]
      service_accounts = [
        "organizations/a1b2c3/serviceAccounts/ci-deployer",
      ]
    }
  ]
}

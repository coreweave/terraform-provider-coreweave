data "coreweave_iam_access_policy_roles" "available" {}

resource "coreweave_iam_access_policy" "automation" {
  organization_id  = "a1b2c3"
  access_policy_id = "automation-deployers"
  description      = "IAM administration for deployment automation"

  rules = [{
    actions = data.coreweave_iam_access_policy_roles.available.roles["IAM Admin"]
    service_accounts = [
      "organizations/a1b2c3/serviceAccounts/ci-deployer",
    ]
  }]
}

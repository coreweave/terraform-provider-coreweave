data "coreweave_iam_service_account" "ci_agent" {
  organization_id = "a1b2c3"
  id              = "ci-agent"
}

output "service_account_uid" {
  value = data.coreweave_iam_service_account.ci_agent.uid
}

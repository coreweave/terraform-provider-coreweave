terraform {
  required_version = ">= 1.14.0"
  required_providers {
    coreweave = {
      source = "coreweave/coreweave"
    }
  }
}

# Authenticate with COREWEAVE_API_TOKEN.
provider "coreweave" {}

variable "runner_id" {
  type        = string
  description = "Identifier of an existing managed runner in your organization."
}

# The action also accepts a reference to a runner managed in the same configuration:
# runner_id = coreweave_sandbox_managed_runner.example.runner_id
# Declaring the action does not automatically invoke it on ordinary applies.
action "coreweave_sandbox_managed_runner_apply_update" "example" {
  config {
    runner_id = var.runner_id
    timeout   = "30m"
  }
}

# After terraform init, invoke explicitly:
# terraform plan -var='runner_id=my-runner' \
#   -invoke=action.coreweave_sandbox_managed_runner_apply_update.example \
#   -out=runner-update.tfplan
# terraform apply runner-update.tfplan
#
# The latest channel version is selected at invocation, not at planning time.
# This bypasses maintenance windows and exclusions, and waits for rollout health.
# To refresh computed status for a runner resource in this configuration afterward:
# terraform apply -refresh-only -var='runner_id=my-runner'

# Purpose: import an existing hot-load operation into Terraform state.
# Prerequisites: Terraform, a configured CoreWeave provider, and an operation UUID.
# Usage: replace the example UUID below, then run this command from the config directory.
terraform import coreweave_inference_hot_load.full 11111111-1111-4111-8111-111111111111

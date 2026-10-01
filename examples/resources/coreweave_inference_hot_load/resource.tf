variable "deployment_id" {
  type        = string
  description = "UUID of a deployment created with hot_load enabled."
}

# Upload the snapshot to the deployment's checkpoint bucket/prefix first.
# Apply submits the operation; refresh observes progress without waiting.
resource "coreweave_inference_hot_load" "full" {
  deployment_id       = var.deployment_id
  identity            = "step1"
  type                = "SNAPSHOT_TYPE_FULL"
  prompt_cache_policy = "PROMPT_CACHE_POLICY_PRESERVE"
}

# After step1 completes, a separate operation can load a delta against it:
# resource "coreweave_inference_hot_load" "incremental" {
#   deployment_id = var.deployment_id
#   identity      = "step2"
#   type          = "SNAPSHOT_TYPE_INCREMENTAL"
#   incremental_snapshot_metadata = {
#     previous_snapshot_identity = "step1"
#     compression_format         = "COMPRESSION_FORMAT_ZSTD"
#     checksum_format            = "CHECKSUM_FORMAT_ADLER32"
#   }
# }

# Destroy cancels an active operation. It does not roll back weights or wait
# for ongoing replica updates to stop. Operation history stays on the server.

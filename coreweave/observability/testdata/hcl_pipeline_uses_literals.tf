resource "coreweave_observability_telemetry_relay_pipeline" "test" {
  slug             = "test-pipeline"
  source_slug      = "test-stream"
  destination_slug = "test-endpoint"
  enabled          = true
}

resource "coreweave_observability_telemetry_relay_endpoint_https" "test" {
  slug         = "test-endpoint"
  display_name = "Test HTTPS Endpoint - test-endpoint"
  endpoint     = "https://telemetry.example.com/ingest"
}

resource "coreweave_observability_telemetry_relay_pipeline" "test" {
  slug             = "test-pipeline"
  source_slug      = "test-stream"
  destination_slug = coreweave_observability_telemetry_relay_endpoint_https.test.slug
  enabled          = true
}

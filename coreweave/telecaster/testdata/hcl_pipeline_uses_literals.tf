resource "coreweave_observability_telemetry_relay_pipeline" "test" {
  spec = {
    source = {
      slug = "test-stream"
    }
    destination = {
      slug = "test-endpoint"
    }
    enabled = true
  }
}

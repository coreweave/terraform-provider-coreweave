resource "coreweave_observability_telemetry_relay_endpoint_https" "test" {
  slug         = "test-https-endpoint"
  display_name = "Test HTTPS Endpoint"
  endpoint     = "https://example.com/telemetry"
}

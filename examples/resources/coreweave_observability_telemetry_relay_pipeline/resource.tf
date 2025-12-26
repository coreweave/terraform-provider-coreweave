# Pipeline forwarding logs to HTTPS endpoint
resource "coreweave_observability_telemetry_relay_pipeline" "example" {
  slug             = "logs-to-https"
  source_slug      = "logs-customer-cluster"
  destination_slug = "my-https-endpoint"
  enabled          = true
}

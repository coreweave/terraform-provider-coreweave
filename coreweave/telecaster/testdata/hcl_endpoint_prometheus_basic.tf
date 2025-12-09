resource "coreweave_observability_telemetry_relay_endpoint_prometheus" "test" {
  slug         = "test-prometheus-endpoint"
  display_name = "Test Prometheus Endpoint"
  endpoint     = "https://prometheus.example.com/api/v1/write"
}

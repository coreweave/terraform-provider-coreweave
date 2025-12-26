# Prometheus Remote Write endpoint with bearer token (e.g., for Grafana Cloud)
resource "coreweave_observability_telemetry_relay_endpoint_prometheus" "example" {
  slug         = "my-prometheus-endpoint"
  display_name = "My Prometheus Endpoint"
  endpoint     = "https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push"

  credentials = {
    bearer_token = {
      token = "glc_eyJvIjoiNzg5..." # Grafana Cloud token
    }
  }
}

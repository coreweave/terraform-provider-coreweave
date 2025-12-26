# HTTPS endpoint with bearer token authentication
resource "coreweave_observability_telemetry_relay_endpoint_https" "example" {
  slug         = "my-https-endpoint"
  display_name = "My HTTPS Endpoint"
  endpoint     = "https://logs.example.com/ingest"

  credentials = {
    bearer_token = {
      token = "my-bearer-token-value"
    }
  }
}

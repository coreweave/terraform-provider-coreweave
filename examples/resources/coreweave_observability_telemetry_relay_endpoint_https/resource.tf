# HTTPS endpoint with custom header authentication.
#
# Credential values are write-only: Terraform sends them but never stores or
# reads them back. Increment credentials_version whenever you change one,
# otherwise the next apply leaves the stored credentials as they are.
resource "coreweave_observability_telemetry_relay_endpoint_https" "example" {
  slug         = "my-https-endpoint"
  display_name = "My HTTPS Endpoint"
  endpoint     = "https://logs.example.com/ingest"
  compression  = "COMPRESSION_TYPE_GZIP"

  credentials_version = 1
  credentials = {
    auth_headers = {
      headers = {
        "X-API-Key" = var.telemetry_api_key
      }
    }
  }
}

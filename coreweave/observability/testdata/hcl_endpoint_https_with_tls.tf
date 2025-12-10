resource "coreweave_observability_telemetry_relay_endpoint_https" "test_tls" {
  slug         = "test-https-tls"
  display_name = "Test HTTPS with TLS"
  endpoint     = "https://example.com/telemetry"
  tls = {
    certificate_authority_data = "LS0tLS1CRUdJTi=="
  }
}

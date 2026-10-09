# Pipeline forwarding logs to an HTTPS endpoint.
#
# Reference the stream and the endpoint rather than hardcoding their slugs.
# CreatePipeline checks synchronously that the endpoint exists, so a literal
# slug leaves no dependency edge and races the endpoint's creation.
data "coreweave_observability_telemetry_relay_stream" "customer_logs" {
  slug = "logs-customer-cluster"
}

resource "coreweave_observability_telemetry_relay_endpoint_https" "logs" {
  slug         = "my-https-endpoint"
  display_name = "My HTTPS Endpoint"
  endpoint     = "https://logs.example.com/ingest"
}

resource "coreweave_observability_telemetry_relay_pipeline" "example" {
  slug             = "logs-to-https"
  source_slug      = data.coreweave_observability_telemetry_relay_stream.customer_logs.slug
  destination_slug = coreweave_observability_telemetry_relay_endpoint_https.logs.slug
  enabled          = true

  # Omit zone_slugs to forward from every zone with an active cluster
  # registration. Listing zones is immutable: changing the set replaces the
  # pipeline.
  zone_slugs = ["us-east-04a"]
}

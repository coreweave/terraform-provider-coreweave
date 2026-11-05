# Pipeline forwarding logs to an HTTPS endpoint.
#
# The endpoint is referenced rather than named by a literal, because
# CreatePipeline checks synchronously that it exists and a literal slug leaves
# no dependency edge to order the two creations.
#
# Streams are platform-defined and always exist, so the data source is not
# there to sequence anything. It fails the plan early on a slug the
# organization cannot read, and it exposes zones_active below.
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

# Forward from wherever the stream currently has data, instead of naming zones
# by hand. zones_active is the only part of a stream that is not static, so
# this is what the data source is for.
#
# zone_slugs is immutable, so a zone appearing or disappearing replaces the
# pipeline. Prefer omitting zone_slugs entirely unless the set has to be
# pinned.
resource "coreweave_observability_telemetry_relay_pipeline" "all_active_zones" {
  slug             = "logs-to-https-all-zones"
  source_slug      = data.coreweave_observability_telemetry_relay_stream.customer_logs.slug
  destination_slug = coreweave_observability_telemetry_relay_endpoint_https.logs.slug

  zone_slugs = keys(data.coreweave_observability_telemetry_relay_stream.customer_logs.zones_active)
}

resource "coreweave_observability_telemetry_relay_endpoint_s3" "test" {
  slug         = "test-s3-endpoint"
  display_name = "Test S3 Endpoint"
  bucket       = "s3://my-telemetry-bucket"
  region       = "us-east-1"
}

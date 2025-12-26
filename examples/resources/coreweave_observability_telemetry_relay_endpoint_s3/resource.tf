# S3 endpoint with AWS credentials
resource "coreweave_observability_telemetry_relay_endpoint_s3" "example" {
  slug         = "my-s3-endpoint"
  display_name = "My S3 Endpoint"
  bucket       = "s3://my-telemetry-bucket"
  region       = "us-east-1"

  credentials = {
    access_key_id     = "AKIAIOSFODNN7EXAMPLE"
    secret_access_key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
  }
}

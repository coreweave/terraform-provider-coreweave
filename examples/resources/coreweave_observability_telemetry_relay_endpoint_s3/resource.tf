# S3 endpoint with AWS credentials.
#
# S3 credentials are mandatory and cannot be removed once set. To rotate them,
# change the values and increment credentials_version in the same apply.
resource "coreweave_observability_telemetry_relay_endpoint_s3" "example" {
  slug         = "my-s3-endpoint"
  display_name = "My S3 Endpoint"
  uri          = "s3://my-telemetry-bucket/logs/"
  region       = "us-east-1"

  credentials_version = 1
  credentials = {
    access_key_id     = var.telemetry_access_key_id
    secret_access_key = var.telemetry_secret_access_key
  }
}

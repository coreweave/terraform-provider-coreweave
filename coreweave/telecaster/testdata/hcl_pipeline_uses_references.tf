resource "coreweave_telecaster_forwarding_endpoint_https" "test" {
  slug         = "test-endpoint"
  display_name = "Test HTTPS Endpoint - test-endpoint"
  endpoint     = "http://telecaster-console.us-east-03-core-services.int.coreweave.com:9000/"
}

resource "coreweave_telecaster_forwarding_pipeline" "test" {
  spec = {
    source = {
      slug = "test-stream"
    }
    destination = {
      slug = coreweave_telecaster_forwarding_endpoint_https.test.slug
    }
    enabled = true
  }
}

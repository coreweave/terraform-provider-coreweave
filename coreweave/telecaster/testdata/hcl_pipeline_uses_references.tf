resource "coreweave_telecaster_forwarding_endpoint" "test" {
  ref = {
    slug = "test-endpoint"
  }
  spec = {
    display_name = "Test HTTPS Endpoint - test-endpoint"
    https = {
      endpoint = "http://telecaster-console.us-east-03-core-services.int.coreweave.com:9000/"
    }
  }
}

resource "coreweave_telecaster_forwarding_pipeline" "test" {
  spec = {
    source = {
      slug = "test-stream"
    }
    destination = {
      slug = coreweave_telecaster_forwarding_endpoint.test.ref.slug
    }
    enabled = true
  }
}

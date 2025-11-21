resource "coreweave_telecaster_forwarding_pipeline" "test" {
  spec = {
    source = {
      slug = "test-stream"
    }
    destination = {
      slug = "test-endpoint"
    }
    enabled = true
  }
}

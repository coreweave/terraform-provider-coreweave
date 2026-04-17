resource "coreweave_inference_gateway" "example" {
  name  = "my-gateway"
  zones = ["US-EAST-04A"]

  auth = {
    core_weave = {}
  }

  routing = {
    body_based = {
      api_type = "OPENAI"
    }
  }
}

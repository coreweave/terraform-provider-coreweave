resource "coreweave_inference_gateway" "example" {
  name  = "my-gateway"
  zones = ["US-EAST-04A"]

  auth = {
    coreweave = {}
  }

  routing = {
    body_based = {
      api_type = "API_TYPE_OPENAI"
    }
  }
}

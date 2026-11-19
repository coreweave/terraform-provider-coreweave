resource "coreweave_inference_gateway" "example" {
  name  = "my-gateway"
  zones = ["US-EAST-04A"]

  auth = {
    coreweave = {
      # Optional: authenticate a different organization (requires API entitlement).
      # organizations = ["abcdef"]
    }
  }

  routing = {
    body_based = {
      api_type = "API_TYPE_OPENAI"
    }
  }
}

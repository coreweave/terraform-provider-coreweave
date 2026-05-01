resource "coreweave_inference_capacity_claim" "example" {
  name = "my-capacity-claim"

  resources = {
    instance_id    = "h100-80gb-sxm5"
    instance_count = 2
    capacity_type  = "CAPACITY_TYPE_SERVERLESS"
    zones          = ["US-EAST-04A"]
  }
}

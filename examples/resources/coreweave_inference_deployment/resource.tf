# Look up available parameters first (optional but recommended).
data "coreweave_inference_parameters" "params" {}

resource "coreweave_inference_deployment" "example" {
  name        = "my-llm"
  gateway_ids = [data.coreweave_inference_parameters.params.gateway_ids[0]]

  runtime = {
    engine  = "vllm"
    version = "0.8.5"
    engine_config = {
      "max-model-len" = "8192"
    }
  }

  resources = {
    instance_type = "H100_80GB_SXM5"
    gpu_count     = 1
  }

  model = {
    name   = "meta-llama/Llama-3.1-8B"
    bucket = "my-model-bucket"
    path   = "models/llama-3.1-8b"
  }

  autoscaling = {
    min              = 1
    max              = 4
    priority         = 100
    capacity_classes = ["RESERVED", "ON_DEMAND"]
    concurrency      = 16
  }

  traffic = {
    weight = 100
  }
}

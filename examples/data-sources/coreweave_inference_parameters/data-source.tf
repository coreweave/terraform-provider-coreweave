data "coreweave_inference_parameters" "params" {}

output "gateway_ids" {
  value = data.coreweave_inference_parameters.params.gateway_ids
}

output "instance_types" {
  value = data.coreweave_inference_parameters.params.instance_types
}

output "vllm_versions" {
  value = data.coreweave_inference_parameters.params.runtime_versions["vllm"].versions
}

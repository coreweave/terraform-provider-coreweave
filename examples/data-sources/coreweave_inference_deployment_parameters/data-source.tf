data "coreweave_inference_deployment_parameters" "deploy_params" {}

output "gateway_ids" {
  value = data.coreweave_inference_deployment_parameters.deploy_params.gateway_ids
}

output "instance_types" {
  value = data.coreweave_inference_deployment_parameters.deploy_params.instance_types
}

output "vllm_versions" {
  value = data.coreweave_inference_deployment_parameters.deploy_params.runtime_versions["vllm"].versions
}

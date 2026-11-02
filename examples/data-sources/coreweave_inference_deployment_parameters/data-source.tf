data "coreweave_inference_deployment_parameters" "deploy_params" {}

output "gateway_ids" {
  value = data.coreweave_inference_deployment_parameters.deploy_params.gateway_ids
}

output "instance_types" {
  value = data.coreweave_inference_deployment_parameters.deploy_params.instance_types
}

# Versions that can be used for new deployments.
output "vllm_stable_versions" {
  value = [
    for version, opts in data.coreweave_inference_deployment_parameters.deploy_params.runtime_engine_options["vllm"].versions :
    version if opts.lifecycle == "LIFECYCLE_STABLE"
  ]
}

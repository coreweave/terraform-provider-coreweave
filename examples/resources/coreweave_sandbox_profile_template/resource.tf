resource "coreweave_sandbox_profile_template" "default" {
  display_name = "default-cpu"
  description  = "Default CPU profile for CI sandboxes."

  spec = {
    container_image = "ghcr.io/coreweave/sandbox-runtime:v1"
    runtime_class   = "kata-qemu"

    resource_defaults = {
      cpu_request    = "500m"
      memory_request = "1Gi"
      cpu_limit      = "2"
      memory_limit   = "4Gi"
    }

    instance_types = ["cpu.small", "cpu.medium"]
    tags           = ["ci"]

    node_selector = {
      "workload-class" = "general"
    }
  }

  labels = {
    team = "platform"
    env  = "prod"
  }
}

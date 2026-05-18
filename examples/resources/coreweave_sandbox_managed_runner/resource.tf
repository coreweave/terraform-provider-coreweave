resource "coreweave_sandbox_profile_template" "default" {
  display_name = "default-cpu"
  description  = "Default CPU profile for sandboxes on this runner."

  spec = {
    runtime_class = "kata-qemu"
    resource_defaults = {
      cpu_request    = "500m"
      memory_request = "1Gi"
      cpu_limit      = "2"
      memory_limit   = "4Gi"
    }
  }
}

resource "coreweave_sandbox_managed_runner" "prod_east" {
  runner_id    = "prod-east-managed"
  display_name = "Prod East (managed)"
  zone         = "US-EAST-04A"
  cluster_name = "prod-east"

  release_channel         = "RELEASE_CHANNEL_STABLE"
  enforce_resource_limits = true

  maintenance_policy = {
    windows = [
      {
        cron             = "0 2 * * SAT"
        duration_seconds = 7200
      }
    ]
  }

  overrides = {
    node_selector = {
      "workload-class" = "general"
    }
    resources = {
      cpu_request    = "2"
      memory_request = "4Gi"
      cpu_limit      = "4"
      memory_limit   = "8Gi"
    }
    scaling = {
      autoscaling_enabled = true
      min_replicas        = 1
      max_replicas        = 5
    }
  }

  profile_bindings = [
    {
      profile_template_id = coreweave_sandbox_profile_template.default.id
      is_default          = true
    },
  ]
}

provider "coreweave" {}

resource "coreweave_sandbox_managed_runner" "test" {
  runner_id       = "test-runner"
  runner_group_id = "development"
  cluster_id      = "00000000-0000-4000-8000-000000000001"
  zone            = "us-east-04a"
  display_name    = "Development runner"

  spec = {
    release_channel         = "RELEASE_CHANNEL_RAPID"
    enforce_resource_limits = true
    maintenance_policy = {
      windows    = [{ cron = "0 2 * * SAT", duration_seconds = 3600 }]
      exclusions = [{ start_time = "2026-12-24T01:00:00+01:00", end_time = "2026-12-26T00:00:00.000Z", reason = "Holiday freeze" }]
    }
    overrides = {
      node_selector     = { "node.coreweave.cloud/instance-type" = "cd-gp-i64-erapids" }
      tolerations       = [{ key = "dedicated", operator = "Equal", value = "sandbox", effect = "NoSchedule" }]
      resources         = { cpu_request = "1", memory_request = "2Gi", cpu_limit = "4", memory_limit = "8Gi" }
      annotations       = { "example.com/team" = "platform" }
      labels            = { team = "platform" }
      env               = { TEST_SETTING = "value", EMPTY_SETTING = "" }
      args              = ["--log-level=info"]
      scaling           = { replicas = 2, autoscaling_enabled = true, min_replicas = 2, max_replicas = 4 }
      cpu_runtime_class = "kata"
      gpu_runtime_class = "kata-nvidia"
    }
    volumes        = { enabled = true, csi_driver_allowlist = ["csi.vastdata.com"] }
    tenant_metrics = { enabled = true, image = "example.com/metrics:v1" }
    image_pull     = { policy = "SANDBOX_IMAGE_PULL_POLICY_ALWAYS" }
    data_plane = {
      load_balancer = {
        scope                   = "RUNNER_DATA_PLANE_SERVICE_SCOPE_PRIVATE"
        load_balancer_class     = "example.com/internal"
        annotations             = { "example.com/purpose" = "sandbox" }
        source_ranges           = ["10.0.0.0/8"]
        external_traffic_policy = "RUNNER_DATA_PLANE_EXTERNAL_TRAFFIC_POLICY_LOCAL"
        hostname                = "runner.example.com"
      }
    }
  }

  policy = {
    display_name = "Development policy"
    base         = jsonencode({ metadata = { labels = { team = "platform" } } })
    constraints = {
      resources = {
        max_cpu     = "8", max_memory = "32Gi", min_cpu = "100m", min_memory = "128Mi"
        default_cpu = "1", default_memory = "1Gi", max_gpu_count = 0
        cpu_ceiling = "16", memory_ceiling = "64Gi", require_limits = true
      }
      image = { allowed_registries = ["example.com/"], allowed_images = ["example.com/workload:stable"] }
      network = {
        allowed_egress = [
          { any = true, ports = [{ protocol = "TCP", port = 443, end_port = 443 }] },
          { cidr = { cidr = "10.0.0.0/8", except = ["10.1.0.0/16"] } },
          { dns_name = "*", dns_name_except = ["blocked.example.com"] },
          { tenant = "TENANT_SCOPE_SAME_ORG" },
          { selector = { pod_labels = { app = "database" }, namespace_labels = { team = "platform" } } },
        ]
        default_egress  = [{ any = true, ports = [{ protocol = "TCP", port = 443 }] }]
        deny_dns        = false
        allowed_ingress = [{ any = true }, { cidr = { cidr = "10.0.0.0/8" } }]
        default_ingress = [{ tenant = "TENANT_SCOPE_SAME_USER" }]
      }
      security = {
        allow_privileged          = true
        allowed_capabilities      = ["NET_ADMIN"]
        allowed_seccomp_profiles  = ["RuntimeDefault"]
        allowed_runtime_classes   = ["kata", "kata-nvidia"]
        default_cpu_runtime_class = "kata"
        default_gpu_runtime_class = "kata-nvidia"
      }
      instance  = { allowed_instance_types = ["cd-gp-i64-erapids"] }
      lifecycle = { default_lifetime_seconds = 3600 }
      metadata  = { denied_annotation_prefixes = ["internal.example.com/"], max_annotation_count = 20 }
      volumes   = { allowed_media = ["STORAGE_MEDIUM_DISK"], max_size = "100Gi" }
    }
    control_plane_access = { source_ip_allowlist = { cidrs = ["2001:db8::/32", "203.0.113.0/24"] } }
  }
}

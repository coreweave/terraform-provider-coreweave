# Requires a Sandbox API deployment implementing full v1 managed-runner CRUD.
# Configure COREWEAVE_API_TOKEN with sandbox_admin access. COREWEAVE_API_ENDPOINT
# can select a different API deployment when needed.
provider "coreweave" {}

variable "cluster_id" {
  type        = string
  description = "CKS cluster UUID for the managed runner."
}

variable "zone" {
  type        = string
  description = "Cluster geographic zone, for example us-east-04a."
}

resource "coreweave_sandbox_managed_runner" "development" {
  runner_id       = "development"
  runner_group_id = "engineering"
  display_name    = "Engineering development"
  cluster_id      = var.cluster_id
  # The Sandbox API returns lowercase zones; normalize values supplied by CKS.
  zone = lower(var.zone)

  spec = {
    release_channel = "RELEASE_CHANNEL_STABLE"
    maintenance_policy = {
      windows = [{ cron = "0 2 * * SAT", duration_seconds = 3600 }]
    }
    overrides = {
      resources = {
        cpu_request    = "1"
        memory_request = "2Gi"
        cpu_limit      = "4"
        memory_limit   = "8Gi"
      }
      scaling = { replicas = 2 }
    }
    image_pull = { policy = "SANDBOX_IMAGE_PULL_POLICY_ALWAYS" }
    volumes = {
      enabled              = true
      csi_driver_allowlist = ["csi.vastdata.com"]
    }
    data_plane = { disabled = true }
  }

  policy = {
    display_name = "Development constraints"
    constraints = {
      resources = {
        default_cpu    = "1"
        default_memory = "2Gi"
        max_cpu        = "8"
        max_memory     = "32Gi"
        max_gpu_count  = 0 # Explicitly forbid GPUs; omitting this removes the cap.
      }
      lifecycle = { default_lifetime_seconds = 3600 }
      volumes   = { max_size = "100Gi" }
    }
    # To restrict public API sources, supply your organization's actual CIDRs:
    # control_plane_access = {
    #   source_ip_allowlist = { cidrs = ["203.0.113.0/24"] }
    # }
    # source_ip_allowlist = {} denies every public source. Omitting the
    # source_ip_allowlist object leaves source IP access unrestricted.
  }
}

resource "coreweave_cks_node_pool" "gpu_workers" {
  cluster_id    = coreweave_cks_cluster.default.id
  name          = "gpu-workers"
  instance_type = "gd-8xh100ib-i128"
  target_nodes  = 2

  compute_class = "default"
  autoscaling   = true
  min_nodes     = 1
  max_nodes     = 4

  labels = {
    workload = "training"
  }

  annotations = {
    "example.com/managed-by" = "terraform"
  }

  taints = [
    {
      key    = "nvidia.com/gpu"
      value  = "true"
      effect = "NoSchedule"
    },
  ]

  update_strategy = "OnSpecUpdate"
}

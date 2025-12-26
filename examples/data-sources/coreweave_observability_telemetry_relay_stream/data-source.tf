# Read information about a metrics stream
data "coreweave_observability_telemetry_relay_stream" "platform_metrics" {
  slug = "metrics-platform"
}

# Read information about a logs stream
data "coreweave_observability_telemetry_relay_stream" "customer_logs" {
  slug = "logs-customer-cluster"
}

# Read audit logs stream
data "coreweave_observability_telemetry_relay_stream" "audit_logs" {
  slug = "logs-audit-kube-api"
}

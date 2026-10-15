# Permanent key. Use a positive duration (for example, 86400) for an expiring key.
resource "coreweave_object_storage_access_key" "example" {
  duration_seconds = 0
  attributes = {
    application = "training"
  }
}

output "access_key_id" {
  value = coreweave_object_storage_access_key.example.id
}

# Sensitive outputs and secrets are still persisted in Terraform state.
output "secret_key" {
  value     = coreweave_object_storage_access_key.example.secret_key
  sensitive = true
}

# Manual rotation:
# terraform apply -replace=coreweave_object_storage_access_key.example
# For an imported key, omit duration_seconds to retain the existing key.
# Adding duration_seconds to an imported resource plans replacement.

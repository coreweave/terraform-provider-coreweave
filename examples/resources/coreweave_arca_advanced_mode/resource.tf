resource "coreweave_arca_advanced_mode" "platform" {
  github_installation_id = var.github_installation_id
  repository             = "https://github.com/acme/platform-config"
  branch                 = "main"
  manifest_path          = ".arca/apps.yaml"

  # Keep false unless destroying this resource should also remove applications
  # currently managed through .arca/apps.yaml.
  allow_destroy_apps = false

  lifecycle {
    prevent_destroy = true
  }
}

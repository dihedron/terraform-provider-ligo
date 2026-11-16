data "ligo_charter" "now" {}

output "charter_sha256" {
  value = data.ligo_charter.now.sha256
}

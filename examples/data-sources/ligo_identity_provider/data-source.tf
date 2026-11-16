data "ligo_identity_provider" "directory" {
  name = "openldap-prod"
}

output "type" {
  value = data.ligo_identity_provider.directory.type
}

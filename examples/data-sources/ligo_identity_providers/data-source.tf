data "ligo_identity_providers" "all" {}

output "names" {
  value = [for i in data.ligo_identity_providers.all.items : i.name]
}

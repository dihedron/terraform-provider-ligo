data "ligo_sources" "ledger" {
  jurisdiction = "dc/corp-prod/*"
  trust_domain = "prod.lab"
  limit        = 500 # an error, not a truncation, when there are more
}

output "names" {
  value = [for s in data.ligo_sources.ledger.items : s.name]
}

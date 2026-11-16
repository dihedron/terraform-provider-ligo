data "ligo_source" "ledger_ci" {
  name = "ledger-ci"
}

output "owner" {
  # null when nobody owns the source (it is not managed by a pipeline)
  value = data.ligo_source.ledger_ci.managed_by
}

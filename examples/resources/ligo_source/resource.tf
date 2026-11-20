# The workloads of a pipeline. Owned by this automation (the lock): only it
# changes the source; a person who tries is refused with 409 resource-managed.
resource "ligo_source" "ledger_ci" {
  name         = "ledger-ci"
  display_name = "Ledger CI"
  trust_domain = "prod.lab"
  svid_regex   = "^spiffe://prod\\.lab/ns/fin/sa/ledger-ci$"

  annotations = {
    team = "fin"
  }
}

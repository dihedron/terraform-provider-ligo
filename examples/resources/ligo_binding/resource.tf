# The access that a source has to a target. Owned by this automation (the lock).
# The apply waits until the binding is ACTIVE; removal revokes the access.
# The parameters name credentials by reference, never by value.
resource "ligo_binding" "ledger_app_reads" {
  name         = "ledger-app-reads-ledger-db"
  source       = ligo_source.ledger_app.name
  target       = ligo_target.ledger.name
  access_level = "read"
  description  = "Month-end reports"
  expires_at   = "2027-06-30T00:00:00Z"

  parameters = jsonencode({
    connectionLimit = 5
  })
}

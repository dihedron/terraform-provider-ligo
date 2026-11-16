# A database that workloads reach through bindings. Owned by this automation
# (the lock). Credentials are named by reference in the platform, never here.
resource "ligo_target" "ledger" {
  name         = "ledger-db"
  display_name = "Ledger database"
  platform     = "pg-ledger"
  jurisdiction = "dc/corp-prod/ledger"

  definition = jsonencode({
    database = "ledger"
    schema   = "ledger"
  })

  access_levels = {
    read = {
      grants = jsonencode({ roles = ["LEDGER_RO_ROLE"] })
    }
    write = {
      grants      = jsonencode({ roles = ["LEDGER_RW_ROLE"] })
      description = "Reads and writes"
    }
  }
}

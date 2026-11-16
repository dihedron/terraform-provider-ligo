# A PostgreSQL platform that wires an identity provider and a secret store. A
# person decides every change to an instance, so each apply step waits for an
# approver (approval_timeout in the provider): the references make Terraform
# create the two instances first, and retire the platform before them.
resource "ligo_platform" "ledger" {
  name         = "pg-ledger"
  jurisdiction = "dc/corp-prod"
  realm        = "prod"
  type         = "postgres.v1"

  allowed_source_trust_domains = ["prod.example.org"]
  target_jurisdictions         = ["dc/corp-prod/*"]
  principal_prefix             = "svc_"

  identity_provider = {
    type     = ligo_identity_provider.directory.type
    instance = ligo_identity_provider.directory.name
  }
  secret_store = {
    type                    = ligo_secret_store.vault.type
    instance                = ligo_secret_store.vault.name
    secret_storage_strategy = "vault-static"
  }

  svid_selector = {
    match_expr        = "path == \"/ligo/plugin/postgres\""
    jwt_audience      = ["https://vault.example.org"]
    allow_shared_svid = true
  }

  # The bootstrap credential is a reference to a path in a store, never a value.
  config = jsonencode({
    host              = "pg.example.org"
    port              = 5432
    database          = "ledger"
    allowed_endpoints = ["pg.example.org:5432"]
    bootstrap_credential = {
      source       = "vault"
      address      = "https://vault.example.org:8200"
      auth_mount   = "jwt-spiffe"
      role         = "ligo-plugin-postgres"
      jwt_audience = "https://vault.example.org"
      kv_path      = "kv/data/ligo/bootstrap/postgres"
    }
  })
}

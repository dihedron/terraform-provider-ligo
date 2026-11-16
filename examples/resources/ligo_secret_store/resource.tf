# A Vault that the platforms keep their credentials in. A person decides every
# change to an instance, so the apply waits for an approver (approval_timeout in
# the provider); an instance is never managed. The config names the bootstrap
# credential by reference (a path in a store), never by value.
resource "ligo_secret_store" "vault" {
  name         = "vault-prod"
  jurisdiction = "dc/platform"
  realm        = "prod"
  type         = "vault.v1"
  display_name = "Production Vault"

  svid_selector = {
    match_expr        = "path == \"/ligo/plugin/vault\""
    jwt_audience      = ["https://vault.example.org"]
    allow_shared_svid = false
  }

  config = jsonencode({
    address               = "https://vault.example.org:8200"
    auth_mount            = "jwt-spiffe"
    role                  = "ligo-plugin-vault"
    workload_jwt_audience = "https://vault.example.org"
    allowed_endpoints     = ["vault.example.org:8200"]
  })
}

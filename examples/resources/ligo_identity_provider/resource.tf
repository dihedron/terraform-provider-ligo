# A directory that the platforms know people and workloads by. A person decides
# every change to an instance, so the apply waits for an approver
# (approval_timeout in the provider). The config names the bootstrap credential
# by reference, never by value.
resource "ligo_identity_provider" "directory" {
  name         = "openldap-prod"
  jurisdiction = "dc/platform"
  realm        = "prod"
  type         = "openldap.v1"

  svid_selector = {
    match_expr   = "path == \"/ligo/plugin/openldap\""
    jwt_audience = ["https://vault.example.org"]
  }

  config = jsonencode({
    url               = "ldaps://ldap.example.org:636"
    service_ou_dn     = "ou=ligo-accounts,dc=example,dc=org"
    allowed_endpoints = ["ldap.example.org:636"]
    bootstrap_credential = {
      source       = "vault"
      address      = "https://vault.example.org:8200"
      auth_mount   = "jwt-spiffe"
      role         = "ligo-plugin-openldap"
      jwt_audience = "https://vault.example.org"
      kv_path      = "kv/data/ligo/bootstrap/openldap"
    }
  })
}

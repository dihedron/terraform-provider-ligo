terraform {
  required_providers {
    ligo = {
      source = "dihedron/ligo"
    }
  }
}

# A pipeline: it authenticates with its SPIFFE identity (X.509-SVID from the
# Workload API) and verifies the server by its SPIFFE ID. Trusted automation
# owns what it files (the lock, managed = true by default) and approves by
# delegation, which needs the upstream record in approval_reference.
provider "ligo" {
  server_url         = "https://ligo.example.org:9443"
  spiffe_socket      = "unix:///run/spire/agent.sock"
  server_id          = "spiffe://prod.example.org/ns/sys/sa/ligo"
  approval_reference = "CHG-0001"
  approval_timeout   = "10m"
}

# A person: LDAP Basic credentials, the password read from a private file
# (never from the configuration). A person cannot own a resource, so every
# resource needs managed = false, and a change waits for an approver.
provider "ligo" {
  alias         = "person"
  server_url    = "https://ligo.example.org:9443"
  ca_cert       = "/etc/ligo/ca.pem"
  username      = "olivia"
  password_file = "/home/olivia/.ligo/password"
  managed       = false
}

# Prerequisites

## On the ligo side

- A running `ligo` v0.7.0 or later, whose admin API (port 9443 by default) is reachable from the machine that runs Terraform.
- For a pipeline: its SPIFFE identity registered as `trusted_automation` on the jurisdictions it changes (the owner, the source, the target and the platform). `ligo` decides this, not the provider. The attested workload is the binary that runs the provider, which is Terraform or OpenTofu, a generic CLI: register it with the narrowest SPIFFE ID and the narrowest jurisdictions that do the job, because anything that runs as that identity has that reach.
- For a person: an LDAP account with the roles that cover the jurisdictions.
- People who approve: every jurisdiction involved needs approvers, or a run that waits never ends.

## On the runner

- Terraform 1.x or OpenTofu 1.x.
- For a pipeline: a SPIFFE Workload API socket (for example the SPIRE agent's) that issues the pipeline an X.509-SVID.
- For a person: a file that holds the LDAP password and that nobody but its owner may read (mode `0600`), and the CA bundle that verifies `ligo` if the system roots do not.

## What the provider never needs

No password, token or private key goes into the configuration or the state. The provider has no attribute that holds one.

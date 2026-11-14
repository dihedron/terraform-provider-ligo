# The ligo Terraform and OpenTofu provider

- **Version:** 0.0 (2026-11-14), placeholder: not a specification
- **Backlog:** `ligo/.specs/ligo-backlog.md`, item 8 (the direction set by the user on 2026-11-08 and the points to discuss are there)
- **Depends on:** `ligo/.specs/ligo-adminclient.md`, the Go admin API client the provider is built on; the same client serves the MCP server (backlog item 11)

Nothing here is to be implemented until this file holds an approved specification with a threat model (AGENTS.md rules 1 and 3). It must settle at least: what `apply` waits for; managed-by-automation resources and the server changes they need; how a pipeline's review becomes the upstream approval of a delegated side; drift; the provider's authentication to `ligo` (SVID, never a stored password); the resources and data sources; acceptance tests with Terraform and OpenTofu.

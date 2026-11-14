# The ligo Terraform and OpenTofu provider

- **Version:** 0.0 (2026-11-14), placeholder: not a specification
- **Backlog:** `ligo/.specs/ligo-backlog.md`, item 8 (the direction set by the user on 2026-11-08 and the points to discuss are there)
- **Depends on:** `ligo/.specs/ligo-adminclient.md`, the Go admin API client the provider is built on; the same client serves the MCP server (backlog item 11)

## Decisions so far (the user, 2026-11-14)

1. **Instances are in scope.** The provider covers identity provider and secret store instances (`.specs/ligo-instances.md` in ligo), first as data sources, then as managed resources, in later chunks.
2. **Instance resources only file the request and wait for approval.** Instance changes are four-eyes change requests in their jurisdiction, never automatic, delegated or break-glass (ligo threat T-22), so the provider cannot approve them. An `apply` files the request, then waits for it with a time-out. While a request is pending, `plan` shows it and does not file a second one. A request that is rejected, withdrawn or expired fails the `apply`.
3. **The bounds stay with the server.** Plugin types and their `instance_bounds` are in the root-owned `server.yaml`; the provider cannot widen them, and has no resource for them (at most a read-only data source).
4. **No secrets in the configuration or in the state.** Bootstrap credentials of an instance are references, as in ligo; the resource schema has no attribute that holds a password, and the plugin schemas of ligo refuse unknown keys.

## Chunks (outline; to be fixed when the spec is approved)

| Chunk | Content |
|---|---|
| 0 | This spec, approved, with its threat model. |
| 1 | The provider's configuration: server URL, server verification, authentication to ligo (SVID), on the Go admin client of ligo (`pkg/adminclient`). |
| 2 | Read-only data sources: realms, platforms, sources, targets, bindings (what the API exposes). |
| 3 | **Instance data sources**: identity provider and secret store instances, read-only. |
| 4 | Managed resources: sources, targets, bindings (the settled behaviour of `apply`, drift and managed-by-automation). |
| 5 | **Instance managed resources**: file the change request and wait for approval, with time-out and pending-request handling (decision 2). |
| 6 | Acceptance tests with Terraform and OpenTofu against a lab ligo; the registry documentation (`tfplugindocs`) and the PDF guides. |

Chunks 4 and 5 depend on the open points of backlog item 8 (managed-by-automation, delegation) and are not started before they are settled.

## Open

Nothing below is implemented until this file holds an approved specification with a threat model (AGENTS.md rules 1 and 3).

It must settle at least: what `apply` waits for; managed-by-automation resources and the server changes they need; how a pipeline's review becomes the upstream approval of a delegated side; drift; the provider's authentication to `ligo` (SVID, never a stored password); the resources and data sources; acceptance tests with Terraform and OpenTofu.

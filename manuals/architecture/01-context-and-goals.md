# Context and goals

`ligo` keeps the catalog of sources, targets and bindings, and decides every change through change requests that the people of each jurisdiction approve (or that a trusted automation approves by delegation). The provider lets infrastructure-as-code drive that, without weakening it.

![The provider in its surroundings.](overview.pdf){width=60%}

## Goals

- Declare `ligo`'s resources in Terraform or OpenTofu and converge them, with the same provider binary for both tools.
- Never become a way around approval: the provider cannot approve, decline, ratify, revert or lift a hold.
- Be safe to re-run: a crash, a time-out or a retry must not file a change twice.
- Hold no secret: no attribute, state entry or log line carries a credential.

## Non-goals

- Approving on behalf of people, or choosing between delegation and a person: `ligo` decides the basis.
- Managing accounts, or anything the admin API does not expose.
- Hiding `ligo`'s refusals: a problem is shown as it is, with its type and detail.

## The client

The provider is a thin layer over `pkg/adminclient`, the admin API client of `ligo`, pinned to a `ligo` version (v0.7.0 for the first release). It adds no HTTP code. The client verifies the server by its SPIFFE ID against the Workload API's bundle, or by a CA file; there is no option to skip it.

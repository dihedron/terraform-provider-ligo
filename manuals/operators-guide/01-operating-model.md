# The operating model

A run of the provider is a run of change requests. For each resource that the plan changes, the provider files one request, then waits until it is applied (a binding: until it is `ACTIVE`), and only then records the change in the state.

![One write: file once, wait, record only what was applied.](apply-sequence.pdf){width=100%}

## Who decides

| Caller | What `ligo` does with its write |
| :----------------------------------------- | :-------------------------------------------- |
| A pipeline registered as `trusted_automation` on every jurisdiction involved | Applies at once (delegation), with the `approval_reference` recorded |
| A person (LDAP) | Waits for the approvers of every side |
| Anyone, for a platform, an identity provider or a secret store | Waits for a person: never delegated |

The provider does not know which applies; it files and waits for what the server asks.

## What the provider never does

It never approves, declines, withdraws, ratifies or reverts a request. It never lifts a hold, never releases a lock and never retries a failed binding. These are people's acts, done with the `ligo` CLI or its web UI.

## What is logged

Errors and log entries carry the problem type and the resource name, never a request body, a header or a definition.

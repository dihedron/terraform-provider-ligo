# Import, drift and failures

## Import

| Resource | Command |
| :------------------------ | :----------------------------------------------------- |
| `ligo_source` | `terraform import ligo_source.x <name>` |
| `ligo_target` | `terraform import ligo_target.x <name>` |
| `ligo_binding` | `terraform import ligo_binding.x <name>` |
| `ligo_platform`, `ligo_identity_provider`, `ligo_secret_store` | `terraform import ligo_platform.x <name>` |

The first plan after an import shows what the configuration would change. A resource the caller may not see is reported as not found: the admin API shows nothing outside the caller's jurisdictions.

## Drift and stale revisions

A refresh reads the resource; a difference is a diff in the next plan. A resource retired by someone else is removed from the state. An update carries the revision in the state; when `ligo` refuses it as stale, the apply fails with:

```text
changed outside Terraform (its revision is not the one in the state): run terraform refresh or plan, and apply again; nothing was retried
```

The provider never rereads the revision to force a write through.

## A failed binding

A binding that ends `FAILED` exists in `ligo`; the provider keeps it in the state and fails the apply, with the phase message. The provider never retries it. Fix the cause (the platform, the parameters), then replace it: `terraform apply -replace=ligo_binding.x`.

## Removal

Removing a resource, or `terraform destroy`, files the retirement (a binding: the revocation) and waits like any other write. A source or target that still has bindings is refused by `ligo`; Terraform's graph removes the bindings first, and a binding that is not Terraform's keeps its source in place on purpose. Use `prevent_destroy` on sources and targets that must never go.

## Instances

Platforms, identity providers and secret stores always wait for a person, and are never managed. Create them in order (an identity provider and a secret store before the platform that names them). The plan does not check that the wired instances are ACTIVE: `ligo` checks at the filing and at the approval. After a platform is approved its plugin starts a moment later, so the provider waits up to a minute for the read-back. Retiring an instance that something uses is refused with `instance-in-use`: retire what uses it first. A caller who may not file on the jurisdiction reads no definition; the state keeps its `config` and `revision`, and a warning says so.

## Rate limiting

`ligo` limits a principal to about ten requests a second, and Terraform applies ten resources at a time. The provider spaces the calls that start a write (the lookup of an open request, the filing) across the whole run, and when `ligo` answers 429 it holds every one of them back for `Retry-After` (a second when none is given, at most 30 seconds). A filing refused that way is tried again, at most 3 attempts in all; a lookup, which only reads, at most 5. Nothing else is retried: a 503, a reset or a time-out may have been processed, and the next apply finds the open request instead.

## A create whose answer was lost

If a replica dies, or the runner is cut off, after `ligo` applied a create and before Terraform saved it, the resource exists and the state does not know it. The next apply finds the name taken. When the pipeline's own applied request for that name says what the configuration says, the provider adopts it and files nothing; otherwise it stops with `exists in ligo, and this run did not create it`, and a person decides whether to `terraform import` it. A person's runs (LDAP) are never adopted this way: the provider cannot tell who they were.

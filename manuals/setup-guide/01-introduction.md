# Introduction

`terraform-provider-ligo` is the provider of `ligo`, the SPIFFE binding lifecycle service. It lets a Terraform or OpenTofu configuration declare the resources of `ligo`: sources, targets and bindings, and the platforms, identity providers and secret stores that serve them. The provider works the same with Terraform and with OpenTofu.

Every change that `ligo` accepts is a **change request**. The provider files it once and waits for it. It never approves, declines or ratifies a request, and it never lifts a hold: those are people's acts, and the provider has no call that does them.

This guide takes you from nothing to a first applied configuration. The *Operator's Guide* covers what happens in a run that waits for a person, the lock, holds and troubleshooting. The *Architecture and Design* guide explains why the provider is built as it is. The reference of every attribute is the registry reference (the `docs/` directory of the repository, shown by the Terraform and OpenTofu registries).

## What you need to decide first

| Question | Why it matters |
| :--------------------------------- | :---------------------------------------------------- |
| Who runs Terraform? | A pipeline with a SPIFFE identity can be *trusted automation* and is approved by delegation. A person is never trusted automation: every write waits for an approver. |
| What does the pipeline own? | What a pipeline files is *managed* by it (the lock): only that pipeline changes it. |
| Who approves the rest? | Instances (platforms, identity providers, secret stores) always wait for a person, and so does everything a person files. |

## Versions

The provider supports the `ligo` version whose client it is built with and the next minor version: the first release is built with `ligo` v0.7.0 (the first with the lock). A `ligo` that is too old or too new for a call answers with a problem, which the provider shows as it is.

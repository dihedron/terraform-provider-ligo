# terraform-provider-ligo

The Terraform and OpenTofu provider of [ligo](https://github.com/dihedron/ligo), the SPIFFE binding lifecycle service: declare sources, targets and bindings next to the rest of your infrastructure, and let `plan` and `apply` drive `ligo`.

**Status:** the provider reads: the data sources `ligo_source(s)`, `ligo_target(s)`, `ligo_binding(s)`, `ligo_platform(s)`, `ligo_identity_provider(s)`, `ligo_secret_store(s)`, `ligo_catalog_target(s)`, `ligo_charter` and `ligo_holds`, with `managed_by` wherever `ligo` has it. The resources `ligo_source`, `ligo_target`, `ligo_binding`, `ligo_identity_provider`, `ligo_secret_store` and `ligo_platform` write: it files a change request once, waits for it, and never approves (create, update, deprecate, removal, drift, import, the lock; a binding is waited for until it is ACTIVE, and a hold or a break-glass review is told, never worked around). The acceptance tests run on the lab with Terraform and OpenTofu (`ligo`'s `test/lab/lab.sh iac test`). It is released as v0.1.0 on GitHub, built on the Go admin API client of `ligo` (`pkg/adminclient`) at `ligo` v0.7.0; it is not yet published on the Terraform and OpenTofu registries. Its design is in [.specs/](.specs); what is left is in [.specs/ligo-provider-backlog.md](.specs/ligo-provider-backlog.md).

## Use

```hcl
terraform {
  required_providers {
    ligo = {
      source = "dihedron/ligo"
    }
  }
}
```

One provider serves both Terraform and OpenTofu.

## Documentation

The registry reference is in [docs/](docs/). The Setup Guide, the Operator's Guide and the Architecture and Design Guide are written in [manuals/](manuals/) and attached to each release as PDF.

## Build, test, lint

```bash
make            # lint and build
go test ./...   # unit tests
make acceptance # acceptance tests, against a lab ligo
make registry-docs # the registry reference (docs/), from the schemas
make manuals    # the PDF guides (manuals/) into build/manuals/; needs docker or podman
make help       # every target
```

See [AGENTS.md](AGENTS.md) for the development rules (spec-driven, tests first, no secrets, no vulnerable dependencies).

## Licence

AGPL-3.0, as `ligo`.

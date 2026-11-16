# terraform-provider-ligo

The Terraform and OpenTofu provider of [ligo](https://github.com/dihedron/ligo), the SPIFFE binding lifecycle service: declare sources, targets and bindings next to the rest of your infrastructure, and let `plan` and `apply` drive `ligo`.

**Status:** the provider reads: the data sources `ligo_source(s)`, `ligo_target(s)`, `ligo_binding(s)`, `ligo_platform(s)`, `ligo_catalog_target(s)`, `ligo_charter` and `ligo_holds`, with `managed_by` wherever `ligo` has it. There are no resources yet. The acceptance tests run on the lab with Terraform and OpenTofu (`ligo`'s `test/lab/lab.sh iac test`). Its design is in [.specs/](.specs); it waits for the Go admin API client of `ligo` (`pkg/adminclient`, `ligo/.specs/ligo-adminclient.md`).

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

## Build, test, lint

```bash
make            # lint and build
go test ./...   # unit tests
make acceptance # acceptance tests, against a lab ligo
make help       # every target
```

See [AGENTS.md](AGENTS.md) for the development rules (spec-driven, tests first, no secrets, no vulnerable dependencies).

## Licence

AGPL-3.0, as `ligo`.

# terraform-provider-ligo

The Terraform and OpenTofu provider of [ligo](https://github.com/dihedron/ligo), the SPIFFE binding lifecycle service: declare sources, targets and bindings next to the rest of your infrastructure, and let `plan` and `apply` drive `ligo`.

**Status:** scaffold. The provider serves, with no resources yet. Its design is in [.specs/](.specs); it waits for the Go admin API client of `ligo` (`pkg/adminclient`, `ligo/.specs/ligo-adminclient.md`).

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

# Validation

## Layers

| Layer | What it proves |
| :-------------------- | :---------------------------------------------- |
| Unit tests with a fake `ligo` | Every resource and data source, the write path, the lock, holds, 429 retry, pending recognition, the called-methods guard |
| HCL acceptance tests | Terraform and OpenTofu runs of complete configurations against the fake |
| Fuzz and property tests | JSON normalisation, form validation |
| Lab tests (build tag `lab`) | The provider on `ligo1` against a real `ligo`, with Terraform and OpenTofu: pipeline, target, binding, instances approved by a person |
| Chaos and campaign scenarios | A restart of `ligo` during a wait, a replica killed during an apply, approval after the time-out, a provider crash after filing |
| Documentation tests | Every resource, data source and attribute is in these guides; every quoted message is in the code; the registry reference is regenerated and checked in CI |

## Versioning with ligo

The provider pins the `ligo` module version it is built with and supports that version and the next minor. The lab runs against the matching `ligo`.

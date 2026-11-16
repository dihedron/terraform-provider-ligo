# Getting and verifying a release

## From a registry

Declare the provider; `terraform init` or `tofu init` downloads it and the registry verifies the signed checksums.

```terraform
terraform {
  required_providers {
    ligo = {
      source  = "dihedron/ligo"
      version = "~> 0.1"
    }
  }
}
```

## From a release by hand

Each release of the repository carries, for every platform, `terraform-provider-ligo_<version>_<os>_<arch>.zip`, and also:

| File | What it is |
| :--------------------------------------------- | :--------------------------------- |
| `terraform-provider-ligo_<version>_SHA256SUMS` | The checksums of the archives |
| `terraform-provider-ligo_<version>_SHA256SUMS.sig` | Its detached GPG signature |
| `terraform-provider-ligo_<version>_manifest.json` | The registry manifest (protocol 6) |
| `terraform-provider-ligo-<guide>-<version>.pdf` | These guides |

Verify before you install: import the public key of the release, check the signature of the checksums file, and then the archive.

```bash
gpg --verify terraform-provider-ligo_0.1.0_SHA256SUMS.sig terraform-provider-ligo_0.1.0_SHA256SUMS
sha256sum --check --ignore-missing terraform-provider-ligo_0.1.0_SHA256SUMS
```

The PDF guides are attached to the release and are not part of the signed checksums: the registries read only their own files.

## Mirrors and air-gapped runners

Put the archives in a provider mirror (`provider_installation` in the CLI configuration, `filesystem_mirror` or `network_mirror`) after verifying them as above.

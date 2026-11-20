# terraform-provider-ligo: backlog

Work agreed for later. An item here is not a specification: each needs its own, approved, before any code (AGENTS.md rule 1). Update this file when an item is added, started or done.

| No. | Item | Recorded | Status |
| :-- | :-- | :-- | :-- |
| 1 | Soak run of apply and destroy cycles, before v0.2.0 | 2026-11-18 | Planned (§1) |
| 2 | Release to the Terraform and OpenTofu registries | 2026-11-18 | Waiting for a stable ecosystem (§2) |
| 3 | Benchmark of a plan over 1,000 data source items | 2026-11-16 | Proposed in the spec (§5), not yet accepted (§3) |

## 1. Soak run, before v0.2.0

**Why.** The pre-release campaign (`ligo-prerelease-campaign.md`) ran scenarios once or twice each. It did not run the provider for hours, so a leak (memory, descriptors, sessions to `ligo`), a slow drift between state and server, or a rate limit that is met only after thousands of calls could not have shown. The spec proposed this run in §5; v0.2.0 does not ship without it.

**The run.**
- Repeated apply, refresh, change and destroy cycles of a configuration of sources, targets, instances, a platform and bindings, with Terraform and with OpenTofu in turn, against the lab of `ligo` (`test/lab/lab.sh iac`), for 8 hours, with the lab's usual chaos beside it (a `ligo` restart, a replica killed, an agent restart) at random, seeded and recorded.
- After every cycle the judge compares the server with the truth of the configuration: nothing left over, no duplicated change request, no resource still managed after its removal, no hold or break-glass review worked around.
- The provider process is sampled every minute (resident memory, open descriptors, goroutines through its debug output if it has one, requests per second to `ligo`); the judge fails on growth beyond a bound after warm-up, on any request refused with `429` that the pacer did not retry, and on any cycle that ended inconsistent.
- It follows AGENTS.md rule 14 of `ligo`: written up as a plan, started in an overnight session on the user's word, never in the middle of a working day; the outcome is reported in the morning.

**Before it.** The soak harness: a Go test under a `soak` build tag in `internal/provider`, a `lab.sh iac soak` command in `ligo` (as `lab.sh soak` in netcheck), its spec and threat model (the lab's credentials are generated per run), a 10-minute smoke run of the machinery only.

**To decide.** The bounds of the judge; whether the same run also measures the benchmark of item 3; which `ligo` version the lab runs (the one the provider pins and the next minor, spec §8 point 4).

## 2. Release to the Terraform and OpenTofu registries

**Why.** v0.1.0 is a GitHub release only (signed zips, the checksums, the manifest and the three PDF guides). Publishing to the registries makes the provider installable by every user without a manual mirror, and it is hard to take back: a published version cannot be replaced, and users' lock files pin its checksums.

**Together with PyPI and Maven Central.** The Python SDK's publication on PyPI and the Java SDK's on Maven Central (`ligo` backlog item 22) wait for the same condition and should go out in the same stable moment.

**The user's condition (2026-11-18).** Done only when the whole ecosystem is stable and no more changes are being made to `ligo` and to the SPIFFE plugins (`lxd-spiffe`, `openstack-spiffe`): a provider on a registry is a promise about the `ligo` versions it supports (spec §8 point 4).

**What it needs, when the time comes.**
- The Terraform registry: the repository public with the `terraform-provider-` prefix, the signing key added to the registry account, the release workflow's assets as `terraform-registry-manifest.json` expects.
- The OpenTofu registry: the provider's entry submitted to the OpenTofu registry repository, with the same key.
- The Setup Guide's installation chapter checked against the registry's real addresses; the README's status line changed.
- A decision on the version to publish first (v0.2.0 after the soak, or later).

**Not now.** Nothing is submitted and no key is uploaded until the user says the ecosystem is stable.

## 3. Benchmark of a plan over 1,000 data source items

Proposed in the spec (§5) and not accepted or built. To decide, with item 1 (the soak can measure the same thing under load).

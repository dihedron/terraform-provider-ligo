# The lock, holds and break-glass

The lock is a feature of `ligo` (v0.7.0); the provider sets it when it files, reads it back and shows it. It decides nothing about it.

- **Ownership.** A source, target or binding filed with `managed: true` by a principal holding `trusted_automation` on every jurisdiction involved belongs to that SPIFFE ID (`managedBy`). Only the owner changes it; others get `resource-managed`.
- **A person is not an owner.** `ligo` answers 403 to `managed: true` from a person. The provider fails the plan first, with the way out.
- **Another owner's resource** is an error at plan time, never an overwrite.
- **Break-glass** changes a managed binding, is never ratified and reverts at its time-to-live. It appears as drift; while its review is open, the apply fails with the request ID (`break-glass-open`).
- **Holds.** A binding removed in an emergency leaves a hold; nobody creates it again until a person lifts it (`resource-held`). The provider never retries and never lifts.
- **Adoption** of an imported resource (`managed = true`) needs the consent of the people of its sides: the apply waits.
- **Instances** are never managed and always wait for a person.

The choice behind these rules: a lock that the automation itself could remove would be no lock. Release, lift, ratify and approve stay people's acts, audited by `ligo`.

# The lock: managed resources

What a trusted pipeline files is **managed** by it: `managed_by` holds its SPIFFE ID, and only that pipeline changes the resource. Anyone else, whatever their role, gets `resource-managed` (409) and nothing is filed. Platforms, identity providers and secret stores are never managed.

## The attribute

`managed` exists on sources, targets and bindings. The provider's `managed` is the default, `true` unless set; each resource's own `managed` overrides it.

| Situation | Result |
| :------------------------------------------- | :--------------------------------------------- |
| A person (LDAP) with `managed = true` | The plan fails: a person cannot own. Set `managed = false` or use a SPIFFE identity |
| A resource managed by another pipeline | The plan fails: two pipelines never change one resource. Change it from its owner, or have a person release it |
| `managed = false` on a managed resource | The owner's own release; applied like any other change |
| `managed = true` on an imported resource | Adoption: the people of its sides must consent, so the apply waits |
| The owning pipeline is gone | An administrator releases the lock with a four-eyes request: `ligo source release`, `ligo target release`, `ligo binding release`; then the next apply with `managed = true` adopts it |

## Break-glass

In an emergency a person may change a managed binding through break-glass. The change is never ratified and reverts at its time-to-live. It shows as drift in the next plan, and the apply puts the configuration back. While a break-glass review is open on the resource, the apply fails with:

```text
has an open break-glass change, which a person must have reviewed before ligo takes another change
```

The request is named in the error. A person reviews it first; the provider does not file over it.

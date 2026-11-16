# Troubleshooting

::: landscape

| Message or problem | Means | Do |
| :------------------------------------- | :---------------------------------- | :----------------------------------- |
| `server_url (or LIGO_SERVER_URL): an https:// URL is required` | The provider has no server | Set `server_url` |
| `A person cannot own a resource` | `username` is set and `managed` is true | `managed = false`, or use a SPIFFE identity |
| `Managed by another automation` | The resource's `managed_by` is not this caller | Change it from its owner, or have a person release it |
| `A change request is pending` (warning) | An open request exists for the resource | Wait for it, or ask who decides it |
| `is still PENDING after ...` | Nobody decided in `approval_timeout` | Ask the approvers; apply again |
| `is pending with another content` | A different change is open for the resource | A person decides or withdraws it first |
| `changed outside Terraform` | Stale revision (`revision-conflict`) | Refresh, plan, apply |
| `is held` (`resource-held`) | A person removed it in an emergency | A person lifts the hold: `ligo hold lift` |
| `has an open break-glass change` (`break-glass-open`) | A break-glass change is not yet reviewed | A person reviews it |
| `is still used` (`instance-in-use`) | Targets, bindings or a platform use the instance | Retire them first |
| `ligo refuses the change in its present state` | A lock, a hold, bindings or a pending request | Read the problem's detail |
| `the caller may not file this change` | 403: the role does not cover the jurisdiction | Check the roles and the jurisdictions |
| `rate limited by ligo after N attempt(s)` | 429 beyond the retries | Wait, then apply again |
| `interrupted while waiting for change request` | The run was cancelled | The request stays open; apply again |

:::

## Reading the audit

Every change is audited with the SPIFFE ID of the caller, the `automation_context` and the `approval_reference`. `ligo audit list <kind> <name>` shows it for one resource; the request ID in a provider message is the key (`ligo cr show <id>`).

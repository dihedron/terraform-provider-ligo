# Resources and schema

| Resource | Admin client calls | Notes |
| :------------------------ | :------------------------------------ | :------------------------------------ |
| `ligo_source` | create, update, deprecate, reactivate, retire | The regex may only narrow on update |
| `ligo_target` | create, update, state changes | `definition` and `access_levels` grants are JSON text |
| `ligo_binding` | create, update, delete | Waits until `ACTIVE`; revocation takes no `managed` |
| `ligo_platform`, `ligo_identity_provider`, `ligo_secret_store` | create, update, retire | Always wait for a person; never managed; one shared resource implementation |

Every resource has a singular and a plural data source (and `ligo_catalog_targets`, `ligo_charter`, `ligo_holds`). A plural data source follows the page tokens to the end and stops at a configured limit (default 1000), so a huge catalog cannot exhaust the runner's memory or time.

## Schema rules

- **Free-form parts** (a target's `definition` and grants, a binding's `parameters`, an instance's `config`) are JSON text, normalised before comparison (sorted keys, no insignificant whitespace), so a reformatting is not a diff.
- **Plan-time validation.** A binding's `parameters` are checked against the platform's `bindingParameters` form when the plan can read it. `ligo` offers no schema of an instance's `config`, so that is validated by `ligo` when the change is filed.
- **No attribute holds a credential.** A test walks every schema for attributes that could hold a secret. `config` is sensitive.
- **Names do not change.** A new name is a new resource. For an instance, `name`, `jurisdiction`, `realm` and `type` replace.
- **Computed attributes**: `id`, `revision`, `phase` (bindings), `last_change_request`, `managed_by`.

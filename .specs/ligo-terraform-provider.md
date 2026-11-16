# The ligo Terraform and OpenTofu provider

- **Version:** 0.2 (2026-11-16), draft for the user's approval (v0.1 of 2026-11-15 had the lock as an open point; §3.7 is now written from what `ligo` v0.7.0 shipped, and open points 2 to 4 have their defaults); nothing is implemented until it is approved (AGENTS.md rules 1 and 3).
- **Backlog:** `ligo/.specs/ligo-backlog.md`, item 8
- **Depends on:** `ligo/.specs/ligo-adminclient.md` (approved, built, `pkg/adminclient` in the `ligo` module), and `ligo/.specs/ligo-managed-resources.md` (the lock, ADR-17: built and released in `ligo` v0.7.0, which the provider pins)
- **Decisions of the user (2026-11-14 and 2026-11-15):** instances are in scope, as data sources first and then as resources that file a request and wait; the lock "managed by automation" is specified and built in `ligo` before the provider's resources; the provider authenticates with the SPIFFE Workload API or with LDAP Basic credentials; a removal from the configuration files the deletion and waits for it; the provider is a separate repository, on `terraform-plugin-framework`.

## 1. Why

`ligo` has an admin API, a CLI and a web UI. Teams that describe their infrastructure as code want their sources, targets and bindings to live in the same repository, review and pipeline as the rest of it. The provider lets Terraform and OpenTofu declare those resources and read ligo's catalog, and it must do so without weakening what makes `ligo` worth having: every change is a change request, a person decides what a person must decide, and nothing secret is in a configuration, in a state or in a log.

## 2. Scope

In: the resources `ligo_source`, `ligo_target`, `ligo_binding`, `ligo_platform`, `ligo_identity_provider` and `ligo_secret_store`; the data sources that read them, the catalog, the charter in force and the holds (`ligo_holds`); `terraform import` of every resource; acceptance tests with Terraform and with OpenTofu; the registry reference and the guides.

Out: the charter and `instance_bounds` (root-owned `server.yaml`, T-22 in `ligo`); accounts (an administrator's act, `account_admin`); break-glass, approvals, ratifications and reverts (decisions of people); maintenance mode, audit export and verification, executors (operations, not infrastructure); the plugins' own configuration; Pulumi and other tools that bridge a Terraform provider (they may work, nobody promises it).

The provider never decides. It has no way to approve, decline, ratify or revert a change request or to break glass, and it cannot call the admin client's raw call, which is not importable from outside the `ligo` module (`ligo-adminclient.md` §8.2, T-A4). A test lists the methods of `pkg/adminclient` that the provider calls and fails on one that is not on the list.

## 3. Design

### 3.1 Provider configuration

| Attribute | Meaning |
|---|---|
| `server_url` | The admin API, an `https://` URL (env `LIGO_SERVER_URL`). |
| `server_id` | The SPIFFE ID ligo must present, when it is verified against the bundle of the Workload API. Default: `spiffe://<the caller's trust domain>/ns/sys/sa/ligo`. |
| `ca_cert` | A path to a PEM bundle that verifies the server instead, for callers with no Workload API. |
| `spiffe_socket` | The Workload API socket (env `SPIFFE_ENDPOINT_SOCKET`). With it, the provider presents the caller's SVID over mTLS. |
| `username`, `password_file` | LDAP Basic credentials, without `spiffe_socket`. The password is in a file that nobody but its owner may read (the CLI's rule); the provider has no attribute that holds a password. |
| `approval_timeout` | How long a write waits for its change request (default `10m`, at most `24h`). |
| `approval_reference` | The upstream change record (a ticket, a pipeline run) sent with every filing; required by `ligo` for a delegated approval (ADR-11). |
| `automation_context` | A map (job, workspace, run URL, initiating user) sent as `automationContext`; recorded by `ligo` for traceability, never used for authorization. |

There is no option to skip verifying the server. The client is `pkg/adminclient`; the provider adds no HTTP code of its own.

A run with a SPIFFE principal that holds `trusted_automation` applies at once inside the principal's jurisdictions (`ligo` ADR-11). A run with LDAP credentials is a person: a person is never `trusted_automation`, so each of its writes waits for the approver. The provider does not tell the two apart and does not try to be clever: it files, then waits (§3.3).

### 3.2 Resources and data sources

| Resource | Admin client calls | Notes |
|---|---|---|
| `ligo_source` | `CreateSource`, `UpdateSource`, `Deprecate/Reactivate/RetireSource` | The regex may only narrow on update; a widening is refused by `ligo` and surfaces as the problem it is. |
| `ligo_target` | `CreateTarget`, `UpdateTarget`, state changes | `definition` and `access_levels.grants` are JSON strings (§3.5). |
| `ligo_binding` | `CreateBinding`, `UpdateBinding`, `DeleteBinding` | Waits until the binding is `ACTIVE` (`UntilBindingActive`); `FAILED` is an error, and the binding, which exists in ligo, is kept in the state so that Terraform replaces it. `parameters` is JSON text checked against the platform's `bindingParameters` form. Removal revokes the access (no `managed` in its body) and waits until the binding is gone. A hold (`resource-held`) and an open break-glass review (`break-glass-open`) stop the apply with an error that names who may act; the provider never lifts a hold and never calls `RetryBinding`/`RotateBinding`. |
| `ligo_platform`, `ligo_identity_provider`, `ligo_secret_store` | `Create/Update/Retire` of each kind | Always wait for a person (never delegated, T-22 in `ligo`); an instance names a bootstrap credential, it never holds one. Never managed (no `managed`, no `managed_by`). `name`, `jurisdiction`, `realm` and `type` replace. `config` is sensitive JSON text. Retirement is refused while something uses the instance (`instance-in-use`). A caller who may not file on the jurisdiction reads no definition: the state keeps its `config` and `revision` and a warning says so. A quarantined instance is warned about and left alone. |

Every data source has a singular form (by name) and a plural form with the filters of the admin client (jurisdiction, state, label, platform, `q`); the plural form follows `nextPageToken` to the end and caps what it returns at a configured limit (default 1000) so that a huge catalog cannot exhaust the runner (T-P8). Also `ligo_catalog_targets` (what the caller's sources may bind to, with the charter's outcome per source) and `ligo_charter` (the policy in force and its SHA-256).

Computed attributes on every resource: `id` (the name), `jurisdiction`, `revision`, `state`, `phase` (bindings), `last_change_request` (its ID and status). Names do not change: a new name is a new resource.

### 3.3 Apply: filing and waiting

Every mutation is a change request. The provider files it once and waits for it with `WaitChangeRequest`:

- **APPLIED** ends the apply; for a binding the provider continues until it is `ACTIVE`.
- **PENDING at `approval_timeout`** fails the apply with the request's ID, the sides that are still awaited and a pointer to who approves them (the sides' jurisdictions). The request stays in `ligo` and may still be approved. The state does **not** record the change as made.
- **REJECTED, WITHDRAWN, EXPIRED, FAILED_TO_APPLY** fail the apply with the request's status and its decision comments.
- **The context is cancelled** (an interrupt, a Terraform time-out): the wait ends and no further request is made. The filed request stays, as in every other path.
- **A plan finds a pending request of the same payload** (matched by `payloadSha256` on the resource's open change requests) and reports it as pending instead of planning to file a second one. An `apply` that finds it waits for it rather than filing another one (T-P6). A request of a different payload for the same resource is shown as a conflict that a person resolves.
- **Dependencies between requests** (a binding on a source that is still pending) are Terraform's graph: the provider does not apply a binding before its source is `APPLIED`.

`ligo` refuses a stale `expectedRevision` (`ErrConflict`). The provider sends the revision it has in the state on every update; when it is stale the apply fails with an error that says the resource changed outside Terraform and asks for a refresh. It never retries a write on its own, with one exception: a filing that `ligo` refused as rate limited (`429`, refused before anything is processed) is tried again, at most 3 attempts in all, waiting for `Retry-After` (and not at all when it asks for more than 30 s); a 503, a reset or a timeout is never retried, as it may have been processed. It never rereads the revision to force it through (T-P5).

### 3.4 Removal

Removing a resource from the configuration, or `terraform destroy`, files the deletion (for a source or a target, the retirement; for a binding, the revocation) and waits like every other write. Inside the automation's jurisdictions it applies at once; otherwise it waits for a person. A source or target that still has bindings fails with `ligo`'s `ErrState` problem and is shown with its text: Terraform's graph removes the bindings first, and a binding that is not Terraform's keeps the source in place on purpose.

### 3.5 Schema rules

- **Free-form parts** (a target's `definition` and `grants`, a binding's `parameters`, an instance's `config`) are JSON strings, normalised before comparison (sorted keys, no insignificant whitespace), so that a reformatting is not a diff. They are validated at plan time against the schemas of the platform's plugin (`GetPlatform().UISchemas`) when the server is reachable; offline plans validate what they can and say so.
- **No attribute holds a credential.** An instance's bootstrap secret is a reference as in `ligo` (`.specs/ligo-instances.md`); a schema attribute that could hold a password is a defect. The sensitive flag is set on anything that could carry one by mistake, and `terraform show` of a state is expected to be safe to share.
- **Names, jurisdictions and visibility** follow the admin API: the jurisdiction of a source is derived from its regex and is computed; that of a target is chosen among those the platform allows.
- **The schema is a contract with the users' state.** From the first published release a rename or a removal of an attribute needs the user's choice (AGENTS.md rule 8). Before it, the schema is free to change.

### 3.6 Drift and import

A refresh reads the resource from `ligo` and a difference is a diff in the next plan: someone changed it in the UI, with the CLI or through another pipeline. A resource that is gone (retired by someone else) is removed from the state, as Terraform expects. `terraform import ligo_source.x name` and the same for every other resource reads the resource and its revision into the state; the first plan after it shows what the configuration would change. A resource the caller may not see is `not found` for it (the admin API shows nothing outside the caller's jurisdictions) and the provider reports it that way, without guessing why.

### 3.7 The lock (chunk 4; `ligo` v0.7.0, ADR-17)

The user's direction of 2026-11-08, restated on 2026-11-15: resources that Terraform creates are marked "managed by automation"; people cannot change them, except a binding through break-glass, and a lasting change goes only through the Terraform configuration. The lock is a feature of `ligo`, specified in `ligo/.specs/ligo-managed-resources.md` and released in v0.7.0. The provider sets it when it files a resource, reads it back and shows it; it never decides anything about it. What `ligo` does, and what the provider does on top:

- **Who owns.** A source, target or binding filed with `managed: true` by a principal holding `trusted_automation` on every jurisdiction involved (the owner, the source, the target and the platform) belongs to that principal's SPIFFE ID (`managedBy`). Only the owner changes it; anyone else, whatever the role, gets `409 resource-managed` and nothing is filed. Platforms, identity providers, secret stores and accounts are never managed, so `managed` exists on `ligo_source`, `ligo_target` and `ligo_binding` only.
- **The attribute.** The provider has a default `managed` (true) and each of the three resources has a `managed` attribute that overrides it; `managed_by` is computed on the resources and on their data sources. The provider sends `adminclient.Options.Managed` on every create and update. `managed = false` on a resource that is managed is the owner's own release (`Unmanaged`), applied like any other change.
- **A person is not an owner.** A run with LDAP credentials (`username`) is a person, and `ligo` answers `403` to `managed: true` from a person. When the provider is configured with `username` and a resource would be managed, the plan says so as an error with the way out (`managed = false`, or a SPIFFE identity), instead of letting the apply fail.
- **Another owner's resource.** A resource that `ligo` reports as managed by another SPIFFE ID is an error at plan time, not an overwrite (two pipelines never fight over one resource); the apply surfaces `409 resource-managed` as the problem it is.
- **Break-glass.** A break-glass change of a managed binding is allowed, is never ratified and reverts at its TTL. It appears as drift in the next plan; the apply puts the configuration back, and while a break-glass review is open on the resource the apply fails with the request's ID.
- **Holds.** A binding removed in an emergency (containment by an owner of a side, or a break-glass `DELETE`) leaves a hold, and nobody creates the binding again, the owner included, until a person lifts it (`409 resource-held`). The apply that re-creates it fails with the problem's text and names who may act (`ligo hold list`, `ligo hold lift`, a person who approves a side). The provider never lifts a hold and never releases a lock: that is a person's act, like an approval, and the methods stay off the called-methods list (§5, AC-P8). The data source `ligo_holds` (`ListHolds`, read-only) shows what is holding a pipeline back.
- **Adoption.** Adding `managed = true` to a resource brought in by `terraform import` is an update that the sides' people must consent to (they decide, not the automation): the apply waits like any other write (§3.3).
- **Orphans.** A lock whose automation is gone is released by an administrator through a four-eyes request (`ligo source|target|binding release`); the provider has no part in it. A resource released that way reads `managed_by` empty, and the next apply with `managed = true` adopts it again.

## 4. Threat model

| # | Threat | Mitigation |
|---|---|---|
| T-P1 | A wrong server answers as `ligo` and receives the request (and with LDAP, the password). | `pkg/adminclient` verifies the server by the SPIFFE ID against the Workload API's bundle, or by `ca_cert`; there is no option to skip it. The provider adds no transport of its own. |
| T-P2 | A credential reaches the configuration, the state, a plan or a log. | No attribute holds a password; `password_file` is a path that must be private; instance credentials are references; errors and `tflog` entries carry the problem `type` and the resource name, never a body, header or definition; a test walks every schema for attributes that could hold a secret and every log for the test credential. |
| T-P3 | A pipeline approves its own change. | The provider has no decision method; the admin client's raw call cannot be imported by it; the server refuses a decision by the requester (`four-eyes-violation`). A test lists the client methods the provider uses. |
| T-P4 | A person running Terraform by hand is mistaken for automation, or the reverse. | A person is never `trusted_automation` (ADR-11: SPIFFE principals only); the provider does not decide the basis, `ligo` does; the plan says nothing about approval it cannot know and the apply waits for what the server asks. |
| T-P5 | A stale plan, applied late, overwrites a newer change. | The revision in the state goes as `expectedRevision`; `ErrConflict` fails the apply and asks for a refresh; no automatic retry. |
| T-P6 | A retry, or a crash between filing and waiting, files the change twice. | The client never retries a write, and the provider retries only a refused `429` filing, boundedly (§3.3). The next plan or apply finds the open request of the same `payloadSha256` and waits for it instead. A test kills the provider after the filing. |
| T-P7 | A compromised automation SVID changes or deletes everything it can reach. | The blast radius is the jurisdictions and platforms of its `trusted_automation` binding, and its rate limit; every change is audited with the `automationContext` and the `approvalReference`; the lock (§3.7, built in `ligo` v0.7.0) stops anyone else from changing what it manages. The provider does not widen any of this and documents the registration advice of `ligo` §10.6 (the attested binary is Terraform, a generic CLI). |
| T-P8 | A huge catalog or a hostile server exhausts the runner's memory or time. | The client's 8 MiB body cap, the data sources' limit on items, the context honoured at every call. |
| T-P9 | A removal deletes something shared. | Deletion is a filed request that `ligo` may refuse (`ErrState` while bindings exist); the resource is the user's declared one; the docs say that `prevent_destroy` is Terraform's tool and show it on sources and targets. |
| T-P10 | An instance's `config` carries a secret by mistake. | The plugin schemas of `ligo` refuse unknown keys; `ligo` validates against them when the change is filed (the provider has no way to read an instance schema, so it cannot at plan time); the attribute is sensitive; the docs show references only. |
| T-P11 | A malicious or confused provider release reaches the users. | Signed releases and the checksum manifest the registries need (goreleaser), `make vuln` and `make secrets` before every commit, pinned dependencies, no `go:generate` that downloads at build time. |
| T-P12 | The registry reference or an example publishes a real value. | The examples use the lab's reserved names (`example.org`, `.lab`) and a test greps `docs/` and `examples/` for the shapes of keys and tokens. |
| T-P13 | The generated client or the generator drifts from the contract. | `ligo`'s `make generate-check` and `TestOpenAPI*` guard the contract; the provider's build pins the `ligo` module version, and the acceptance tests run against the matching `ligo`. |
| T-P14 | A hold blocks a pipeline, and its owner works around it by re-filing. | The apply fails with `resource-held`, naming who may lift it; the provider never retries and never lifts; the lift is a person's audited act. |
| T-P15 | A person running the provider sets `managed = true` and gets a surprise `403` after waiting. | The plan fails first, naming the cause and the way out (§3.7). |

## 5. Acceptance criteria and tests first

| # | Criterion | Test |
|---|---|---|
| AC-P1 | The provider verifies the server (SPIFFE ID, `ca_cert`) and has no way to skip it; the password is never in a state, a plan or a log. | Unit tests with a TLS `httptest` server (as `pkg/adminclient`'s) and a hostile one; a walk of every schema; a log scan. |
| AC-P2 | Every resource and data source reads, plans and applies as §3.2 says; `terraform import` works for each. | `terraform-plugin-testing` against the lab, with Terraform and with OpenTofu. |
| AC-P3 | A write waits for its change request: APPLIED, PENDING at the time-out, REJECTED, WITHDRAWN, EXPIRED, FAILED_TO_APPLY, each with its error text; a binding waits for ACTIVE. | Unit tests with a fake admin server; acceptance tests with a person approving through the API. |
| AC-P4 | A pending request of the same payload is found by plan and apply, and not filed twice; a crash after the filing does not duplicate it. | Unit test with the admin server recording requests; an acceptance test that kills the provider process. |
| AC-P5 | A stale revision fails the apply and is not retried. | Unit test; acceptance test with a change made through the CLI between plan and apply. |
| AC-P6 | A removal files the deletion and waits; `ErrState` shows the problem text. | Acceptance test. |
| AC-P7 | Drift, a resource gone, the free-form JSON normalisation. | Unit tests; a fuzz target for the normaliser (it must be idempotent and never lose a key). |
| AC-P8 | The provider calls only the methods on its list, and none that decides. | `TestCalledMethods`: parses the provider's source for calls on `*adminclient.Client`. |
| AC-P9 | A restart of `ligo` during a wait, a replica killed during an apply, and an approval after the time-out leave Terraform and `ligo` consistent. | Lab suite with chaos steps (chunk 6). |
| AC-P10 | The lock (chunk 4): a resource filed by the provider is managed; `managed_by` is read back; a managed resource of another owner is refused at plan time; `managed = false` releases; a person with LDAP credentials and `managed = true` is refused at plan time; `resource-held` and `resource-managed` reach the user with their text. | Unit tests with a fake admin server that returns `managedBy` of another owner and the two problems; acceptance tests against the lab (chunk 4), with a pipeline SVID and a rival. |

Tests are written before the code of each chunk, from this table. Proposed, for the user to accept: a fuzz target for the JSON normaliser, a benchmark of a plan over 1,000 data source items, and a soak run of repeated apply and destroy cycles against the lab with random kills of `ligo`.

## 6. Documentation

The registry reads the reference from `docs/` (generated with `tfplugindocs`, with the examples of every resource and data source). The PDF guides (Setup, Operator's, Architecture and Design) are built from `manuals/<guide>/` and attached to every release (AGENTS.md rule 12): setup covers the provider's installation from the Terraform and the OpenTofu registries, its authentication and the registration of the SVID of the pipeline; the operator's guide covers a run that waits, a pending request, drift, import and what to do after a failed apply; the architecture guide has the threat model of §4 and the flows. `ligo`'s Developer's Guide Part 3 gets a short chapter that points here.

## 7. Chunks

| Chunk | Content |
|---|---|
| 0 | This spec, approved. |
| 1 | (PR #1, `ligo` v0.7.0 pinned) The provider's configuration and the admin client wired in (§3.1); the called-methods test; the acceptance harness with Terraform and OpenTofu against the lab (AC-P1, AC-P8). Started 2026-11-15 on the user's word, after `ligo` v0.6.0, before this spec was reviewed: configuration, session and `TestCalledMethods` are written, tests first (schema, round trip over TLS, wrong server gets no credentials, every refusal, no password in a diagnostic); the SPIFFE Workload API path and the acceptance harness are not yet covered (they need a SPIRE agent and the lab). The `ligo` module is `v0.6.0`. |
| 2 | The read data sources: sources, targets, bindings, platforms, the catalog, the charter, and `ligo_holds`; `managed_by` on every one that has it (AC-P2 for reads). Done on `feature/chunk-2-data-sources`: the acceptance harness runs on the lab's `ligo1` with Terraform and OpenTofu (`lab.sh iac test`, in `ligo`), against a fake server (hermetic) and against the real `ligo` (a pipeline's source with its owner, an unmanaged one, a target, the catalog, the charter). A plural over its `limit` is an error. The collector stops at the limit, and a run of empty pages ends at the 5-minute read timeout, so a server that returns an endless `nextPageToken` cannot hold a plan (T-P8). The examples are checked against the schema and scanned for secrets (T-P12). The registry reference (`tfplugindocs`, which needs a Terraform binary) is generated in chunk 6; the CI does not run the acceptance tests (it has no lab), so the setup actions for the two tools are not added. |
| 3 | The data sources of identity provider and secret store instances. Done on `feature/chunk-3-instance-data-sources`: `ligo_identity_provider(s)` and `ligo_secret_store(s)`; `revision` and `config` are null for a viewer who may not file on the jurisdiction (and for an instance that `server.yaml` defines); `config` is sensitive (T-P10). The lists have no filters, as the admin client has none. |
| 4 | (4c `ligo_binding` done on `feature/chunk-4c-binding`: `source`, `target` and `name` replace; the filing carries no `kind` (ligo sets it); the lab provisions a real binding (`TestLabBinding`); a hold is not made in the lab, as only a person contains a binding: the unit tests and `ligo`'s `serve test-managed` cover it. Chunk 4 is complete. 4b `ligo_target` done on `feature/chunk-4b-target`: `access_levels` is a map keyed by the level's name, each with `grants` (JSON text), `description` and `state`; a level that leaves the map is retired, a new one goes in the update, and an update names every level (the ones it is about to retire and the ones ligo retired before); `definition`, `grants` and `network_flows` are JSON text whose reformatting is no diff; `jurisdiction` is required; the definition and the grants are checked at plan time against the platform's forms (`uiSchemas`: a list of fields, validated by a small own checker, not JSON Schema), and a platform that cannot be read is a warning. Found on the lab: ligo limits a principal to 10 requests a second (burst 20), a long run can meet `429`, which the provider now retries for a filing only (3 attempts, `Retry-After` honoured, see §3.3). 4a `ligo_source` done on `feature/chunk-4a-source`: the write machinery in `internal/provider/write.go`; a pending request is recognised by its payload, as a subset of the stored one, since the provider cannot recompute the server's `payloadSha256`; `managed` is not sent on a state change or a removal, which `ligo` refuses; trusted automation needs the provider's `approval_reference` for the delegated approval; `contacts` and `external_refs` are left out until asked; 4b `ligo_target` and 4c `ligo_binding` follow.) The resources `ligo_source`, `ligo_target`, `ligo_binding` with the apply, removal, drift and import rules; the lock (§3.7), which is built and released in `ligo` v0.7.0 (AC-P2 to P7, P10). |
| 5 | The instance resources, which file and wait (§3.3, `ligo` T-22). 5a `ligo_identity_provider` and `ligo_secret_store` done on `feature/chunk-5a-instances`, on one shared resource type (`instance_resource.go`); the admin API offers no schema of an instance's `config`, so the provider cannot validate it at plan time (T-P10): `ligo` refuses a bad one when the change is filed, before anything waits. The lab files them as `ligo-ops` and the platform approver approves in the background (`TestLabInstances`). 5b `ligo_platform` done on `feature/chunk-5b-platform`: the same shared resource with the platform's own settings (`target_jurisdictions`, `namespace_template`, `check_existence`, `principal_prefix`, `allowed_source_trust_domains`, `rotation_interval`) and its wiring (`identity_provider`, `secret_store`, required); the plan does not check that the wired instances are ACTIVE (one made by the same configuration does not exist yet): `ligo` checks at the filing and at the approval, and the references order the creation and the retirement. Found on the lab: a platform cannot be read until its plugin serves, a moment after the approval, so the read-back after a change waits (up to a minute) while `ligo` says the instance is unavailable. |
| 6 | The lab suite with the chaos steps; the registry reference and the PDF guides; a release (AC-P9). |

## 8. Open points

All settled on 2026-11-16 (the user took the recommended answers):

1. **The lock's design**: settled by `ligo` v0.7.0 and written in §3.7.
2. **Plan-time validation against the platform's schemas** needs the server at plan time; a plan on a runner with no access to `ligo` validates what it can and says that it did not validate the rest. There is no vendored copy of the schemas.
3. **`ligo_account`** is left out: accounts are an administrator's act (`account_admin`).
4. **Versioning with `ligo`:** the provider supports the `ligo` version whose module it pins and the next minor one (at 0.x there is no wider promise). The pinned version is in `go.mod`, the supported ones in the docs and the release notes; a `ligo` that is too old or too new for a call answers with a problem that the provider shows as it is.

Where this spec names a `ligo` command, it is the `ligo <object> <verb>` form of v0.7.0 (`ligo source show`, `ligo binding list`, `ligo hold list`, `ligo source release`).

# Threat model

::: landscape

| ID | Threat | Mitigation |
| :----- | :------------------------------------- | :------------------------------------------------ |
| T-P1 | A wrong server answers as `ligo` and receives the request (with LDAP, the password) | `pkg/adminclient` verifies the server by SPIFFE ID against the Workload API's bundle, or by `ca_cert`; no option skips it |
| T-P2 | A credential reaches the configuration, state, plan or log | No attribute holds a password; `password_file` is a private path; instance credentials are references; logs carry the problem type and the resource name, never a body; a test walks every schema and log |
| T-P3 | A pipeline approves its own change | The provider has no decision method; a test lists the client methods it calls; the server refuses a decision by the requester |
| T-P4 | A person is mistaken for automation, or the reverse | A person is never `trusted_automation`; `ligo` decides the basis; the apply waits for what the server asks |
| T-P5 | A stale plan, applied late, overwrites a newer change | `expectedRevision` from the state; a conflict fails and asks for a refresh; no retry |
| T-P6 | A retry or a crash between filing and waiting files twice | Writes are not retried except a refused 429; the next run finds the open request of the same payload and waits for it |
| T-P7 | A compromised automation SVID changes everything it reaches | Blast radius is its `trusted_automation` jurisdictions and rate limit; every change is audited; the lock stops others; registration advice for the attested binary |
| T-P8 | A huge catalog or a hostile server exhausts the runner | The client's 8 MiB body cap, the data sources' item limit, the context honoured at every call |
| T-P9 | A removal deletes something shared | Deletion is a request `ligo` may refuse while bindings exist; `prevent_destroy` is documented |
| T-P10 | An instance's `config` carries a secret by mistake | Plugin schemas of `ligo` refuse unknown keys at filing; the attribute is sensitive; the docs show references only |
| T-P11 | A malicious or confused provider release reaches users | Signed checksums, `make vuln` and `make secrets` before commits, pinned dependencies and container images, no generator that downloads at build time |
| T-P12 | The registry reference or an example publishes a real value | Reserved names in examples; a test greps `docs/` and `examples/` for key and token shapes |
| T-P13 | The client or the generator drifts from the contract | The `ligo` module version is pinned; acceptance tests run against the matching `ligo` |
| T-P14 | A hold blocks a pipeline and its owner re-files around it | The apply fails with `resource-held`; the provider never retries or lifts |
| T-P15 | A person sets `managed = true` and gets a 403 after waiting | The plan fails first, with the cause and the way out |

:::

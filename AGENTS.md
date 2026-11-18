# AGENTS.md - Project Guidelines

`terraform-provider-ligo` is the Terraform and OpenTofu provider of [ligo](https://github.com/dihedron/ligo), the SPIFFE binding lifecycle service. It drives ligo through `pkg/adminclient`, the Go admin API client of the ligo module; the design of that client is `ligo/.specs/ligo-adminclient.md`.

## Build, Test, and Lint Commands
* **Run Tests:** `go test -v ./...`
* **Run Specific Test:** `go test -v ./internal/provider -run TestSourceResource`
* **Acceptance Tests:** `make acceptance` (`TF_ACC=1`; they drive a real ligo, whose admin URL is in `LIGO_TEST_SERVER_URL`; never against a production server)
* **Build Binary:** `make`
* **Release:** `make release-[major|minor|patch]` depending on whether we are increasing the major, minor or revision level in semantic versioning (e.g. v1.2.3)
* **Other arguments:** run `make help` to get a list of available command line switches.

## Spec-Driven Development (SDD) Workflow
1. **Never code blind:** All new features or non-trivial bug fixes must have an approved specification file in the `.specs/` directory before writing implementation code.
2. **Plan first:** Always use Claude's `/plan` mode to review requirements against the codebase before editing files.
3. **Threat Model:** Whenever developing a complex solution, check that there is a Threat Model in the spec, ask for one otherwise, and take it into account when designing the solution itself and writing the code.
4. **Test-Driven Implementation:** Write unit or integration tests matching the acceptance criteria in the spec *before* writing the implementation code.
5. **Compliance Audit:** Before finishing a task, verify that the implementation matches the API contracts, data models, and edge cases defined in the active spec.
6. **No Secrets in Commits:** Before committing anything, check with all available local tools (e.g. gitleaks, trufflehog: `make secrets`) that no secrets are in the sources being committed.
7. **No vulnerable dependencies in Commits:** Before committing, run govulncheck (`make vuln`) and check that there are no vulnerable dependencies in `go.mod`.
8. **No unneeded backward compatibility:** Backward compatibility is not necessary (and should be avoided if it complicates code, introduces warts or hinders comprehension of the design and the code) until a product's major version (e.g. v1.0.0) is released, and even then it may be worth evaluating costs and benefits of keeping the product backward-compatible versus bumping the product's major version. Always ask for the user's choice unless the decision is trivial. Note that a Terraform provider's schema is a contract with users' state: a rename or a removal of an attribute breaks their configurations, so from the first published release it needs the user's choice.
9. **Fuzzy Testing and Benchmarking:** Propose fuzzy testing and benchmarking when reasonable.
10. **Lab Testing:** Whenever applicable, propose to set up a lab environment to cover complex integration scenarios (here: acceptance tests against a lab `ligo`, with Terraform and with OpenTofu); don't shy away from proposing more coverage scenarios, including lengthy stress tests and chaos monkey to check for correctness under random conditions (e.g. race conditions, malfunctions), whenever reasonable.
11. **Up-to-date Documentation:** Always keep documentation up to date and aligned with the code and the product behaviour.
12. **Publish Documentation:** Always produce and attach to the release a Setup Guide, an Operator's Guide and an Architecture and Design Guide, in PDF format from Markdown sources; when diagrams and tables do not fit a page in portrait mode, use the landscape orientation for that page. Additional documents may be produced on a per-project basis. The Terraform and OpenTofu registries read the provider's reference from `docs/` (generated with `tfplugindocs`), so the sources of the PDF guides live in `manuals/<guide>/`, not in `docs/`.
13. **Sane Commits:** run `go test ./...` before every commit and check that everything is still sane.
14. **Overnight Runs:** Any run that takes hours (soak and chaos runs, full lab rebuilds, release-run campaigns) is written up as a plan and run in an overnight, unattended session, never started in the middle of a working day: propose it, say how long it takes, and report the outcome in the morning.

## Coding Style & Architecture Standards
* **Language/Framework:** Go (Golang); the provider is built on `terraform-plugin-framework` (not the older SDKv2), and tested with `terraform-plugin-testing`; standard library elsewhere; propose additional libraries where applicable.
* **Error Handling:** Never swallow errors. Wrap errors using `fmt.Errorf("context: %w", err)`. In the provider, report failures as diagnostics (`resp.Diagnostics.AddError`) with a summary and a detail that name the resource and the ligo problem `type`; never put a secret in one. Log errors where they occur using `log/slog` (or `tflog` inside the plugin framework's calls).
* **Concurrency:** Use explicit context propagation (`context.Context`) across all network requests and background work, and honour the cancellation Terraform sends.
* **Logging:** Use structured logging with contextual keys (`tenant_id`, `request_id`). Never log raw secrets, tokens, or PII; mark sensitive attributes `Sensitive: true`.
* **State:** Terraform state holds what the provider writes into it: never store a credential, a token or a password there.

## Project Structure Overview
* `cmd/terraform-provider-ligo/` - The provider's entrypoint; run by Terraform or OpenTofu, not by people.
* `internal/` - Private provider code (resources, data sources, the provider itself).
* `pkg/` - Public library code (e.g. the build metadata injected at link time).
* `docs/` - The provider's reference for the registries (generated with `tfplugindocs`).
* `manuals/` - Sources of the PDF guides of the release (rule 12).
* `examples/` - Example configurations, used by the generated documentation and the acceptance tests.
* `.specs/` - Feature design documents and architectural specifications.

## Command line
The provider is a plugin process: Terraform and OpenTofu start it and talk to it over gRPC, so its command line is small. It supports `-version` (a one-liner compact mode) and `-version-full` (verbose: the application, its version, license, author, build and packaging environments), and `-debug` (to attach a debugger, as the plugin framework prescribes). Command line parsing is based on `github.com/jessevdk/go-flags` where flags are needed; follow the convention `my-application <object> <verb>` for any command a person is meant to run.

# Add custom targets below...

#
# compile is the default target; it builds the
# provider for the default platform (linux/amd64)
#
.DEFAULT_GOAL := compile

# GOLANGCI_LINT_VERSION is the golangci-lint the lint target expects: the one
# the golangci-lint workflow runs
GOLANGCI_LINT_VERSION := 2.14

.PHONY: lint
lint: ## check that the code is clean (gofmt, go vet, golangci-lint); every build runs it first
	@echo "Checking the code..."
	@unformatted="$$(gofmt -l cmd internal pkg)"; \
		if [ -n "$$unformatted" ]; then echo "not formatted with gofmt (run gofmt -w):" >&2; echo "$$unformatted" >&2; exit 1; fi
	@go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint is not installed: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLANGCI_LINT_VERSION).0" >&2; exit 1; }
	@golangci-lint version 2>/dev/null | grep -q "version $(GOLANGCI_LINT_VERSION)\." || echo "warning: golangci-lint is not v$(GOLANGCI_LINT_VERSION), which CI runs: its findings may differ" >&2
	@golangci-lint run ./...

.PHONY: compile
compile: lint go-dev ## build for the default linux/amd64 platform

.PHONY: snapshot
snapshot: signing-check lint go-snapshot ## build a snapshot version for the supported platforms (signed if GPG_FINGERPRINT is set)

.PHONY: release
release: signing-check lint ## build a release version (requires a valid tag and the signing key), guides included
	@test -n "$(GPG_FINGERPRINT)" || { echo "a release must be signed: set GPG_FINGERPRINT" >&2; exit 1; }
	@MANUALS_VERSION="$$(git describe --tags --exact-match)" manuals/build.sh
	@$(MAKE) --no-print-directory go-release

.PHONY: manuals
manuals: ## build the PDF guides into build/manuals/ (needs docker or podman)
	@manuals/build.sh

# signing-check: the Terraform and OpenTofu registries require the checksums
# file to be signed with a GPG key whose public half is registered with them
.PHONY: signing-check
signing-check:
	@if [ -n "$(GPG_FINGERPRINT)" ] && ! gpg --list-secret-keys "$(GPG_FINGERPRINT)" >/dev/null 2>&1; then echo "GPG_FINGERPRINT is set but gpg holds no such secret key" >&2; exit 1; fi

.PHONY: clean
clean: ## clean the binary directory
	@rm -rf dist

# FUZZTIME is how long make fuzz runs each fuzz target
FUZZTIME ?= 30s

.PHONY: fuzz
fuzz: ## run every fuzz target for FUZZTIME each (default 30s); go test runs their seeds anyway
	@for pkg in $$(go list ./...); do \
		for target in $$(go test -list '^Fuzz' "$$pkg" | grep '^Fuzz'); do \
			echo "$$pkg $$target"; \
			go test "$$pkg" -run '^$$' -fuzz "^$$target$$" -fuzztime $(FUZZTIME) || exit 1; \
		done; \
	done

# ACCEPTANCE runs the acceptance tests, which drive a real ligo through the
# provider (TF_ACC=1); LIGO_TEST_SERVER_URL and the credentials come from the
# environment, never from the repository
.PHONY: acceptance
acceptance: ## run the acceptance tests against a ligo server (needs LIGO_TEST_SERVER_URL; see .specs)
	@test -n "$$LIGO_TEST_SERVER_URL" || { echo "set LIGO_TEST_SERVER_URL to the admin API of a lab ligo" >&2; exit 1; }
	TF_ACC=1 go test -count=1 -timeout 30m -tags acceptance ./...

# CONTAINER runs the secret scanners; podman works as well
CONTAINER ?= docker

# the scanners run at their latest release; CI pins its versions
# (.github/workflows/ci.yml), and "make scanners" shows when they fall behind:
# versions that differ disagree on findings
GITLEAKS_IMAGE := zricethezav/gitleaks:latest
TRUFFLEHOG_IMAGE := ghcr.io/trufflesecurity/trufflehog:latest

.PHONY: scanners
scanners: ## pull the latest secret scanners and compare their versions with CI's pins (run before every commit)
	@$(CONTAINER) pull -q $(GITLEAKS_IMAGE) >/dev/null
	@$(CONTAINER) pull -q $(TRUFFLEHOG_IMAGE) >/dev/null
	@echo "gitleaks:   latest $$($(CONTAINER) run --rm $(GITLEAKS_IMAGE) version | tr -d v), CI $$(sed -n 's/.*GITLEAKS_VERSION: *//p' .github/workflows/ci.yml)"
	@echo "TruffleHog: latest $$($(CONTAINER) run --rm $(TRUFFLEHOG_IMAGE) --version 2>&1 | awk '{print $$2}'), CI $$(sed -n 's/.*trufflehog@.*# *v//p' .github/workflows/ci.yml)"

.PHONY: secrets
secrets: scanners ## scan the history and the working tree for secrets with gitleaks and TruffleHog, as CI does (run before every commit)
	@echo "gitleaks: history..."
	@$(CONTAINER) run --rm -v "$(CURDIR)":/repo:ro $(GITLEAKS_IMAGE) git /repo --config /repo/.gitleaks.toml --no-banner --redact
	@echo "gitleaks: working tree..."
	@$(CONTAINER) run --rm -v "$(CURDIR)":/repo:ro $(GITLEAKS_IMAGE) dir /repo --config /repo/.gitleaks.toml --no-banner --redact
	@echo "TruffleHog: history..."
	@$(CONTAINER) run --rm -v "$(CURDIR)":/repo:ro $(TRUFFLEHOG_IMAGE) git file:///repo --results=verified,unknown --fail --no-update
	@echo "TruffleHog: working tree..."
	@$(CONTAINER) run --rm -v "$(CURDIR)":/repo:ro $(TRUFFLEHOG_IMAGE) filesystem /repo --exclude-paths=/repo/.trufflehog-exclude --results=verified,unknown --fail --no-update

# govulncheck runs through "go run" so that it is always built with the
# current toolchain; "-scan module" flags every vulnerable module in go.mod,
# called or not, as CI's dependency review does
.PHONY: vuln
vuln: ## check go.mod and the code for known vulnerabilities with govulncheck (run before every commit)
	@rc=0; \
	go run golang.org/x/vuln/cmd/govulncheck@latest -C cmd/terraform-provider-ligo -scan module || rc=1; \
	go run golang.org/x/vuln/cmd/govulncheck@latest ./... || rc=1; \
	exit $$rc

# the registry reference (docs/) is generated from the schemas and the
# examples; the registries read it
.PHONY: registry-docs
registry-docs: ## generate the registry reference (docs/) with tfplugindocs (it fetches a Terraform binary)
	@go tool tfplugindocs generate --provider-name ligo --rendered-provider-name ligo \
		--provider-dir cmd/terraform-provider-ligo --examples-dir ../../examples --rendered-website-dir ../../docs --website-source-dir ../../templates

.PHONY: registry-docs-check
registry-docs-check: registry-docs ## check that docs/ is what the schemas and examples generate
	@git diff --exit-code -- docs || { echo "docs/ is not up to date: run make registry-docs and commit it" >&2; exit 1; }
	@test -z "$$(git ls-files --others --exclude-standard docs)" || { echo "docs/ has files that are not committed: run make registry-docs and commit them" >&2; exit 1; }

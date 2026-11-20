package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// The acceptance tests run Terraform or OpenTofu (TF_ACC=1, and the CLI on the
// PATH or in TF_ACC_TERRAFORM_PATH) against the fake admin API. In the lab
// they run on ligo1: lab.sh iac test.

var protoV6 = map[string]func() (tfprotov6.ProviderServer, error){
	"ligo": providerserver.NewProtocol6WithError(New("test")()),
}

// hcl is the provider block that points at the fake, and body.
func (f *fake) hcl(t *testing.T, body string) string {
	t.Helper()
	return fmt.Sprintf(`
provider "ligo" {
  server_url    = %q
  ca_cert       = %q
  username      = "olga"
  password_file = %q
}
%s`, f.URL, f.ca, passwordFile(t, 0o600), body)
}

func TestAccSourceAndSources(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/sources/ledger-app", 200, detail(sourceJSON("ledger-app", pipeline)))
	f.routes["GET /v1/admin/sources"] = func(r *http.Request) (int, string) {
		if r.URL.Query().Get("pageToken") == "p2" {
			return 200, page(sourceJSON("b", ""), "")
		}
		return 200, page(sourceJSON("a", pipeline), "p2")
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config: f.hcl(t, `
data "ligo_source" "one" { name = "ledger-app" }
data "ligo_sources" "all" { jurisdiction = "dc/*" }
output "owner" { value = data.ligo_source.one.managed_by }
`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.ligo_source.one", "managed_by", pipeline),
				resource.TestCheckResourceAttr("data.ligo_source.one", "trust_domain", "prod.example.org"),
				resource.TestCheckResourceAttr("data.ligo_sources.all", "items.#", "2"),
				resource.TestCheckResourceAttr("data.ligo_sources.all", "items.0.name", "a"),
				resource.TestCheckNoResourceAttr("data.ligo_sources.all", "items.1.managed_by"),
			),
		}},
	})
}

func TestAccNotFoundFailsThePlan(t *testing.T) {
	f := newFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config:      f.hcl(t, `data "ligo_source" "x" { name = "nope" }`),
			ExpectError: regexp.MustCompile(`(?s)Not found.*ligo_source nope`),
		}},
	})
}

func TestAccLimitFailsThePlan(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/sources", 200, page(sourceJSON("a", "")+","+sourceJSON("b", ""), ""))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config:      f.hcl(t, `data "ligo_sources" "all" { limit = 1 }`),
			ExpectError: regexp.MustCompile(`more than 1 items`),
		}},
	})
}

func TestAccBindingsTargetsHoldsCharter(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/bindings", 200, page(bindingJSON("b1"), ""))
	f.on("GET /v1/admin/targets/ledger-db", 200, detail(targetJSON("ledger-db")))
	f.on("GET /v1/admin/charter", 200, `{"policy":{"b":1,"a":2},"sha256":"`+strings.Repeat("c", 64)+`"}`)
	f.on("GET /v1/admin/holds", 200, page("", ""))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config: f.hcl(t, `
data "ligo_bindings" "mine" { source = "ledger-app" }
data "ligo_target" "db" { name = "ledger-db" }
data "ligo_charter" "now" {}
data "ligo_holds" "open" {}
`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.ligo_bindings.mine", "items.0.managed_by", pipeline),
				resource.TestCheckResourceAttr("data.ligo_bindings.mine", "items.0.parameters", `{"a":{"b":2,"z":1},"role":"r"}`),
				resource.TestCheckResourceAttr("data.ligo_target.db", "definition", `{"alpha":{"x":1,"y":2},"zeta":1}`),
				resource.TestCheckResourceAttr("data.ligo_charter.now", "policy", `{"a":2,"b":1}`),
				resource.TestCheckResourceAttr("data.ligo_holds.open", "items.#", "0"),
			),
		}},
	})
}

// TestAccWrongServerGetsNoCredentials (AC-P1, in HCL): a server that the CA
// does not vouch for receives no request, so no password.
func TestAccWrongServerGetsNoCredentials(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/charter", 200, `{"policy":{},"sha256":"x"}`)
	cfg := strings.Replace(f.hcl(t, `data "ligo_charter" "now" {}`), f.ca, strangerCA(t), 1)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(`(?i)certificate|verify|tls`)}},
	})
	for _, r := range f.seen() {
		t.Errorf("the wrong server saw %s", r)
	}
}

// The instances' data sources, and T-P10 in HCL: the config is sensitive, so an
// output that shows it must say so.
func TestAccInstances(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/identity-providers/dir", 200, instanceJSON("IdentityProvider", "dir", "openldap.v1", true))
	f.on("GET /v1/admin/secret-stores", 200, `{"items":[`+instanceJSON("SecretStore", "vault", "vault.v1", false)+`]}`)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{
				Config: f.hcl(t, `
data "ligo_identity_provider" "dir" { name = "dir" }
data "ligo_secret_stores" "all" {}
output "config" { value = data.ligo_identity_provider.dir.config }
`),
				ExpectError: regexp.MustCompile(`(?i)sensitive`),
			},
			{
				Config: f.hcl(t, `
data "ligo_identity_provider" "dir" { name = "dir" }
data "ligo_secret_stores" "all" {}
output "config" {
  value     = data.ligo_identity_provider.dir.config
  sensitive = true
}
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.ligo_identity_provider.dir", "type", "openldap.v1"),
					resource.TestCheckResourceAttr("data.ligo_secret_stores.all", "items.0.type", "vault.v1"),
					resource.TestCheckNoResourceAttr("data.ligo_secret_stores.all", "items.0.config"),
				),
			},
		},
	})
}

// TestAccSourceLifecycle: create, a replan that finds nothing to do, an
// update, drift that the apply puts back, import and removal. The caller is a
// person, so the source is not managed (managed = false).
func TestAccSourceLifecycle(t *testing.T) {
	l := newLigoFake(t)
	cfg := func(desc string) string {
		return l.hcl(t, fmt.Sprintf(`
resource "ligo_source" "ledger" {
  name         = "ledger-app"
  trust_domain = "prod.example.org"
  svid_regex   = "^spiffe://prod\\.example\\.org/ns/fin/sa/.*$"
  description  = %q
  managed      = false
}
`, desc))
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg("v1"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_source.ledger", "id", "ledger-app"),
				resource.TestCheckResourceAttr("ligo_source.ledger", "revision", "1"),
				resource.TestCheckResourceAttr("ligo_source.ledger", "state", "ACTIVE"),
				resource.TestCheckNoResourceAttr("ligo_source.ledger", "managed_by"),
				resource.TestCheckResourceAttrSet("ligo_source.ledger", "last_change_request"),
			)},
			{Config: cfg("v1"), PlanOnly: true}, // idempotent: nothing to file
			{Config: cfg("v2"), Check: resource.TestCheckResourceAttr("ligo_source.ledger", "revision", "2")},
			{
				PreConfig: func() { // somebody changes it in the UI
					l.mu.Lock()
					l.sources["ledger-app"]["description"] = "edited by hand"
					l.sources["ledger-app"]["metadata"].(map[string]any)["revision"] = int64(5)
					l.mu.Unlock()
				},
				Config:             cfg("v2"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: cfg("v2"), Check: resource.TestCheckResourceAttr("ligo_source.ledger", "description", "v2")},
			{ResourceName: "ligo_source.ledger", ImportState: true, ImportStateId: "ledger-app", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore: []string{"managed", "last_change_request"}},
		},
	})
	if l.sources["ledger-app"]["metadata"].(map[string]any)["state"] != "RETIRED" {
		t.Error("destroy must retire the source")
	}
}

// AC-P3: a person cannot own a resource, and the plan says so without calling
// ligo for a filing.
func TestAccPersonCannotOwn(t *testing.T) {
	l := newLigoFake(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config: l.hcl(t, `
resource "ligo_source" "x" {
  name         = "x"
  trust_domain = "prod.example.org"
  svid_regex   = "^x$"
}
`),
			ExpectError: regexp.MustCompile(`(?s)person cannot own`),
		}},
	})
	if l.count() != 0 {
		t.Errorf("%d filings: a refused plan files nothing", l.count())
	}
}

// TestAccTargetLifecycle: create, a reformatting of the JSON that is no diff,
// a level added, deprecated and removed, import and removal.
func TestAccTargetLifecycle(t *testing.T) {
	l := newLigoFake(t)
	platformForms(l)
	cfg := func(def, levels string) string {
		return l.hcl(t, fmt.Sprintf(`
resource "ligo_target" "t" {
  name          = "ledger-db"
  platform      = "pg"
  jurisdiction  = "dc/corp-prod/ledger"
  definition    = %s
  access_levels = {
%s
  }
  managed = false
}
`, def, levels))
	}
	const read = `    read = { grants = jsonencode({ roles = ["RO"] }) }`
	const write = `    write = { grants = jsonencode({ roles = ["RW"] }) }`
	const writeOld = `    write = {
      grants = jsonencode({ roles = ["RW"] })
      state  = "DEPRECATED"
    }`
	tight := `jsonencode({ database = "ledger", schema = "ledger" })`
	loose := `"{\n  \"schema\": \"ledger\",\n  \"database\": \"ledger\"\n}"` // the same, written another way
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg(tight, read), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_target.t", "revision", "1"),
				resource.TestCheckResourceAttr("ligo_target.t", "access_levels.read.state", "ACTIVE"),
			)},
			{Config: cfg(loose, read), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{noChange{}}}}, // a reformatting is no diff
			{Config: cfg(tight, read+"\n"+write), Check: resource.TestCheckResourceAttr("ligo_target.t", "access_levels.write.state", "ACTIVE")},
			{Config: cfg(tight, read+"\n"+writeOld), Check: resource.TestCheckResourceAttr("ligo_target.t", "access_levels.write.state", "DEPRECATED")},
			{Config: cfg(tight, read), Check: resource.TestCheckNoResourceAttr("ligo_target.t", "access_levels.write.grants")},
			{ResourceName: "ligo_target.t", ImportState: true, ImportStateId: "ledger-db", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore: []string{"managed", "last_change_request"}},
			{Config: cfg(`jsonencode({ database = "ledger", schema = "ledger", password = "hunter2" })`, read),
				ExpectError: regexp.MustCompile(`(?s)password is not a field`)},
		},
	})
	if l.targets["ledger-db"]["metadata"].(map[string]any)["state"] != "RETIRED" {
		t.Error("destroy must retire the target")
	}
}

// noChange is an empty-plan check that shows what the plan would change.
type noChange struct{}

func (noChange) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	for _, rc := range req.Plan.ResourceChanges {
		if rc.Change == nil || rc.Change.Actions.NoOp() {
			continue
		}
		b, _ := json.Marshal(map[string]any{"before": rc.Change.Before, "after": rc.Change.After})
		resp.Error = fmt.Errorf("%s would change: %s", rc.Address, b)
	}
}

// TestAccBindingLifecycle: a source, a target and the binding between them in
// one configuration; the binding goes after both and is revoked before them.
func TestAccBindingLifecycle(t *testing.T) {
	l := newLigoFake(t)
	l.on("GET /v1/admin/platforms/pg", 200, `{"apiVersion":"v","kind":"Platform","name":"pg","jurisdiction":"dc/corp-prod","realm":"prod","identityProvider":{"instance":"i","type":"t"},
		"secretStore":{"instance":"s","type":"t"},"allowedSourceTrustDomains":[],"networkFlows":[],"uiSchemas":{"bindingParameters":{"fields":[{"jsonKey":"connectionLimit","fieldType":"UI_FIELD_TYPE_NUMBER"}]},`+
		pgFormsJSON[1:]+`}`)
	cfg := func(params, purpose string) string {
		return l.hcl(t, fmt.Sprintf(`
resource "ligo_source" "app" {
  name         = "ledger-app"
  trust_domain = "prod.example.org"
  svid_regex   = "^spiffe://prod\\.example\\.org/ns/fin/sa/.*$"
  managed      = false
}
resource "ligo_target" "db" {
  name          = "ledger-db"
  platform      = "pg"
  jurisdiction  = "dc/corp-prod/ledger"
  definition    = jsonencode({ database = "ledger", schema = "ledger" })
  access_levels = { read = { grants = jsonencode({ roles = ["RO"] }) } }
  managed       = false
}
resource "ligo_binding" "b" {
  name         = "ledger-app-reads-ledger-db"
  source       = ligo_source.app.name
  target       = ligo_target.db.name
  access_level = "read"
  parameters   = %s
  description  = %q
  managed      = false
}
`, params, purpose))
	}
	tight := `jsonencode({ connectionLimit = 5 })`
	loose := `"{ \"connectionLimit\": 5 }"`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg(tight, "reports"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_binding.b", "phase", "ACTIVE"),
				resource.TestCheckResourceAttr("ligo_binding.b", "revision", "1"),
				resource.TestCheckResourceAttr("ligo_binding.b", "source", "ledger-app"),
			)},
			{Config: cfg(loose, "reports"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{noChange{}}}}, // a reformatting is no diff
			{Config: cfg(`jsonencode({ connectionLimit = 9 })`, "reports"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_binding.b", "revision", "2"),
				resource.TestCheckResourceAttr("ligo_binding.b", "parameters", `{"connectionLimit":9}`),
			)},
			{Config: cfg(`jsonencode({ connectionLimit = "many" })`, "reports"), ExpectError: regexp.MustCompile(`(?s)connectionLimit`)},
			{ResourceName: "ligo_binding.b", ImportState: true, ImportStateId: "ledger-app-reads-ledger-db", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore: []string{"managed", "last_change_request"}},
		},
	})
	if len(l.bindings) != 0 {
		t.Error("destroy must revoke the binding")
	}
	if l.targets["ledger-db"]["metadata"].(map[string]any)["state"] != "RETIRED" || l.sources["ledger-app"]["metadata"].(map[string]any)["state"] != "RETIRED" {
		t.Error("destroy must retire the source and the target, after the binding")
	}
}

// TestAccInstanceLifecycle: an identity provider and a secret store, each
// created only when a person approves, changed, reformatted, imported and
// retired. approveIn stands for the person.
func TestAccInstanceLifecycle(t *testing.T) {
	l := newLigoFake(t)
	l.approveIn = 300 * time.Millisecond
	cfg := func(desc, config string) string {
		return l.hcl(t, fmt.Sprintf(`
resource "ligo_identity_provider" "dir" {
  name         = "ldap"
  jurisdiction = "dc/corp-prod"
  realm        = "prod"
  type         = "openldap.v1"
  config       = jsonencode({ url = "ldaps://ldap.example.org:636" })
}
resource "ligo_secret_store" "vault" {
  name         = "vault-prod"
  jurisdiction = "dc/corp-prod"
  realm        = "prod"
  type         = "vault.v1"
  description  = %q
  svid_selector = {
    match_expr   = "path == \"/ligo/plugin/vault\""
    jwt_audience = ["https://vault.example.org"]
  }
  config = %s
}
`, desc, config))
	}
	tight := `jsonencode({ address = "https://vault.example.org", role = "ligo" })`
	loose := `"{ \"role\": \"ligo\", \"address\": \"https://vault.example.org\" }"`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg("v1", tight), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_secret_store.vault", "revision", "1"),
				resource.TestCheckResourceAttr("ligo_identity_provider.dir", "revision", "1"),
				resource.TestCheckResourceAttr("ligo_secret_store.vault", "svid_selector.match_expr", `path == "/ligo/plugin/vault"`),
			)},
			{Config: cfg("v1", loose), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{noChange{}}}}, // a reformatting is no diff
			{Config: cfg("v2", tight), Check: resource.TestCheckResourceAttr("ligo_secret_store.vault", "revision", "2")},
			{ResourceName: "ligo_secret_store.vault", ImportState: true, ImportStateId: "vault-prod", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore: []string{"last_change_request"}},
		},
	})
	if len(l.instances) != 0 {
		t.Errorf("destroy must retire the instances: %v", l.instances)
	}
}

// TestAccPlatformLifecycle: a platform wired to an identity provider and a
// secret store of the same configuration is created after them and retired
// before them, every step approved by a person.
func TestAccPlatformLifecycle(t *testing.T) {
	l := newLigoFake(t)
	l.approveIn = 300 * time.Millisecond
	cfg := func(prefix string) string {
		return l.hcl(t, fmt.Sprintf(`
resource "ligo_identity_provider" "dir" {
  name         = "ldap"
  jurisdiction = "dc/corp-prod"
  realm        = "prod"
  type         = "openldap.v1"
}
resource "ligo_secret_store" "vault" {
  name         = "vault-prod"
  jurisdiction = "dc/corp-prod"
  realm        = "prod"
  type         = "vault.v1"
}
resource "ligo_platform" "pg" {
  name         = "pg-ledger"
  jurisdiction = "dc/corp-prod"
  realm        = "prod"
  type         = "postgres.v1"
  principal_prefix = %q
  identity_provider = { type = ligo_identity_provider.dir.type, instance = ligo_identity_provider.dir.name }
  secret_store      = { type = ligo_secret_store.vault.type, instance = ligo_secret_store.vault.name }
  config            = jsonencode({ host = "pg.example.org" })
}
`, prefix))
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg("svc_"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_platform.pg", "revision", "1"),
				resource.TestCheckResourceAttr("ligo_platform.pg", "check_existence", "true"),
				resource.TestCheckResourceAttr("ligo_platform.pg", "target_jurisdictions.0", "dc/corp-prod/*"),
			)},
			{Config: cfg("svc_"), PlanOnly: true},
			{Config: cfg("app_"), Check: resource.TestCheckResourceAttr("ligo_platform.pg", "revision", "2")},
			{ResourceName: "ligo_platform.pg", ImportState: true, ImportStateId: "pg-ledger", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore: []string{"last_change_request"}},
		},
	})
	if len(l.instances) != 0 {
		t.Errorf("destroy must retire the instances: %v", l.instances)
	}
	var retired []string
	for _, id := range l.order {
		if cr := l.crs[id]; cr["operation"] == "DELETE" {
			retired = append(retired, cr["resourceType"].(string))
		}
	}
	if len(retired) != 3 || retired[0] != "PLATFORM" {
		t.Errorf("the platform goes before the instances it wires: %v", retired)
	}
}

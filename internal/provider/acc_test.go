package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
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

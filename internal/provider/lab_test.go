//go:build lab

package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// The lab tests read what a real ligo holds: lab.sh iac test creates a source
// owned by the pipeline and one that nobody owns, and passes their names, the
// server and the files of the person who reads (a person with full access).

func labEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s is not set: run this through lab.sh iac test", name)
	}
	return v
}

func TestLabReads(t *testing.T) {
	server, socket, id := labEnv(t, "LIGO_E2E_ADMIN"), labEnv(t, "LIGO_LAB_SOCKET"), labEnv(t, "LIGO_LAB_SERVER_ID")
	managed, free := labEnv(t, "LIGO_LAB_MANAGED_SOURCE"), labEnv(t, "LIGO_LAB_FREE_SOURCE")
	owner := labEnv(t, "LIGO_LAB_OWNER")
	cfg := fmt.Sprintf(`
provider "ligo" {
  server_url    = %q
  spiffe_socket = %q
  server_id     = %q
}
data "ligo_source" "managed" { name = %q }
data "ligo_source" "free"    { name = %q }
data "ligo_sources" "dc" { jurisdiction = "dc/*" }
data "ligo_charter" "now" {}
data "ligo_platforms" "all" {}
data "ligo_holds" "open" {}
data "ligo_target" "t" { name = %q }
data "ligo_catalog_targets" "all" {}
data "ligo_bindings" "all" { limit = 5000 }
data "ligo_identity_provider" "dir" { name = "openldap-prod" }
data "ligo_secret_store" "vault" { name = "vault-prod" }
data "ligo_identity_providers" "all" {}
data "ligo_secret_stores" "all" {}
`, server, socket, id, managed, free, labEnv(t, "LIGO_LAB_TARGET"))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.ligo_source.managed", "managed_by", owner),
				resource.TestCheckNoResourceAttr("data.ligo_source.free", "managed_by"),
				resource.TestCheckResourceAttr("data.ligo_source.managed", "trust_domain", "prod.lab"),
				resource.TestMatchResourceAttr("data.ligo_charter.now", "sha256", regexp.MustCompile(`^[0-9a-f]{64}$`)),
				resource.TestCheckResourceAttr("data.ligo_platforms.all", "items.0.serving", "true"),
				resource.TestCheckResourceAttr("data.ligo_target.t", "platform", "pg-ledger"),
				resource.TestCheckResourceAttr("data.ligo_identity_provider.dir", "type", "openldap.v1"),
				resource.TestCheckResourceAttr("data.ligo_identity_provider.dir", "realm", "prod"),
				resource.TestCheckResourceAttr("data.ligo_secret_store.vault", "type", "vault.v1"),
				resource.TestMatchResourceAttr("data.ligo_identity_providers.all", "items.#", regexp.MustCompile(`^[1-9]`)),
				resource.TestMatchResourceAttr("data.ligo_secret_stores.all", "items.#", regexp.MustCompile(`^[1-9]`)),
				resource.TestMatchResourceAttr("data.ligo_catalog_targets.all", "items.#", regexp.MustCompile(`^[1-9]`)),
			),
		}},
	})
}

func labProvider(t *testing.T, timeout string) string {
	t.Helper()
	return fmt.Sprintf(`
provider "ligo" {
  server_url       = %q
  spiffe_socket    = %q
  server_id        = %q
  approval_timeout = %q
  # trusted automation is approved by delegation, which needs the upstream record
  approval_reference = "CHG-IAC-LAB"
}
`, labEnv(t, "LIGO_E2E_ADMIN"), labEnv(t, "LIGO_LAB_SOCKET"), labEnv(t, "LIGO_LAB_SERVER_ID"), timeout)
}

func labSource(name, regex, description string, managed string) string {
	return fmt.Sprintf(`
resource "ligo_source" "s" {
  name         = %q
  trust_domain = "prod.lab"
  svid_regex   = %q
  description  = %q
  %s
}
`, name, regex, description, managed)
}

// TestLabPipeline runs as the pipeline (trusted automation): its writes apply
// at once, it owns what it files and releases it (AC-P2), and the removal
// retires the source.
func TestLabPipeline(t *testing.T) {
	suffix := labEnv(t, "LIGO_E2E_SUFFIX")
	owner := labEnv(t, "LIGO_LAB_OWNER")
	name, regex := "iac-p-"+suffix, `^spiffe://prod\.lab/ns/climg/sa/wp`+suffix+`$`
	cfg := func(desc, managed string) string { return labProvider(t, "2m") + labSource(name, regex, desc, managed) }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg("v1", ""), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_source.s", "managed_by", owner),
				resource.TestCheckResourceAttr("ligo_source.s", "state", "ACTIVE"),
				resource.TestCheckResourceAttr("ligo_source.s", "revision", "1"),
			)},
			{Config: cfg("v1", ""), PlanOnly: true},
			{Config: cfg("v2", ""), Check: resource.TestCheckResourceAttr("ligo_source.s", "revision", "2")},
			{Config: cfg("v2", "managed = false"), Check: resource.TestCheckNoResourceAttr("ligo_source.s", "managed_by")},
			{ResourceName: "ligo_source.s", ImportState: true, ImportStateId: name, ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore: []string{"managed", "last_change_request"}},
		},
	})
}

// TestLabPending runs as an operator, who is not trusted automation: its
// request waits for a person. The first phase (LIGO_LAB_PHASE=file) ends at the
// timeout with the request still open; the second (adopt) is run while a person
// approves, and waits for that same request instead of filing another (AC-P4,
// T-P6). lab.sh iac test does the approving and counts the requests.
func TestLabPending(t *testing.T) {
	phase := os.Getenv("LIGO_LAB_PHASE")
	if phase == "" {
		t.Skip("run by lab.sh iac test, in two phases")
	}
	name, regex := labEnv(t, "LIGO_LAB_PENDING_SOURCE"), `^spiffe://prod\.lab/ns/adopt/sa/w`+labEnv(t, "LIGO_E2E_SUFFIX")+`p$`
	switch phase {
	case "file":
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: protoV6,
			Steps: []resource.TestStep{{Config: labProvider(t, "5s") + labSource(name, regex, "pending", "managed = false"),
				ExpectError: regexp.MustCompile(`(?s)still\s+PENDING.*awaiting`)}},
		})
	case "adopt":
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: protoV6,
			Steps: []resource.TestStep{{Config: labProvider(t, "2m") + labSource(name, regex, "pending", "managed = false"),
				Check: resource.TestCheckResourceAttr("ligo_source.s", "revision", "1")}},
		})
	default:
		t.Fatalf("phase %q", phase)
	}
}

// TestLabTarget runs as the pipeline: a target on the lab's pg-ledger platform
// with its access levels added, deprecated and retired, checked against the
// platform's form before anything is filed. A long run meets ligo's rate limit
// (10 requests a second): the provider's bounded retry of a refused filing
// absorbs it.
func TestLabTarget(t *testing.T) {
	suffix := labEnv(t, "LIGO_E2E_SUFFIX")
	owner := labEnv(t, "LIGO_LAB_OWNER")
	name := "iac-tg-" + suffix
	cfg := func(def, levels, managed string) string {
		return labProvider(t, "2m") + fmt.Sprintf(`
resource "ligo_target" "t" {
  name          = %q
  platform      = "pg-ledger"
  jurisdiction  = "dc/corp-prod/ledger"
  definition    = %s
  access_levels = {
%s
  }
  %s
}
`, name, def, levels, managed)
	}
	const def = `jsonencode({ database = "ledger", schema = "ledger" })`
	const read = `    read = { grants = jsonencode({ roles = ["LEDGER_RO_ROLE"] }) }`
	const write = `    write = { grants = jsonencode({ roles = ["LEDGER_RW_ROLE"] }) }`
	const writeOld = `    write = {
      grants = jsonencode({ roles = ["LEDGER_RW_ROLE"] })
      state  = "DEPRECATED"
    }`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg(def, read, ""), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_target.t", "managed_by", owner),
				resource.TestCheckResourceAttr("ligo_target.t", "access_levels.read.state", "ACTIVE"),
				resource.TestCheckResourceAttr("ligo_target.t", "revision", "1"),
			)},
			{Config: cfg(def, read, ""), PlanOnly: true},
			{Config: cfg(def, read+"\n"+write, ""), Check: resource.TestCheckResourceAttr("ligo_target.t", "access_levels.write.state", "ACTIVE")},
			{Config: cfg(def, read+"\n"+writeOld, ""), Check: resource.TestCheckResourceAttr("ligo_target.t", "access_levels.write.state", "DEPRECATED")},
			{Config: cfg(def, read, ""), Check: resource.TestCheckNoResourceAttr("ligo_target.t", "access_levels.write.grants")},
			{Config: cfg(`jsonencode({ database = "ledger", schema = "ledger", password = "hunter2" })`, read, ""), ExpectError: regexp.MustCompile(`(?s)password is not a field`)},
			{Config: cfg(def, read, "managed = false"), Check: resource.TestCheckNoResourceAttr("ligo_target.t", "managed_by")},
			{ResourceName: "ligo_target.t", ImportState: true, ImportStateId: name, ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore: []string{"managed", "last_change_request"}},
		},
	})
}

// TestLabBinding runs as the pipeline: a source, a target and the binding
// between them, which is provisioned for real (ACTIVE), changed, and revoked
// when the test ends. A hold is not made here: only a person can contain a
// binding, which the unit tests cover and ligo's own e2e (serve test-managed)
// proves against the real server.
func TestLabBinding(t *testing.T) {
	suffix := labEnv(t, "LIGO_E2E_SUFFIX")
	owner := labEnv(t, "LIGO_LAB_OWNER")
	cfg := func(limit int) string {
		return labProvider(t, "3m") + fmt.Sprintf(`
resource "ligo_source" "s" {
  name         = "iac-bs-%[1]s"
  trust_domain = "prod.lab"
  svid_regex   = "^spiffe://prod\\.lab/ns/climg/sa/wb%[1]s$"
}
resource "ligo_target" "t" {
  name          = "iac-bt-%[1]s"
  platform      = "pg-ledger"
  jurisdiction  = "dc/corp-prod/ledger"
  definition    = jsonencode({ database = "ledger", schema = "ledger" })
  access_levels = { read = { grants = jsonencode({ roles = ["LEDGER_RO_ROLE"] }) } }
  network_flows = jsonencode([{ name = "ledger", purpose = "DATA", protocol = "TCP", destination = "pg.ligo.lab", ports = [5432], tls = true, serverName = "pg.ligo.lab" }])
}
resource "ligo_binding" "b" {
  name         = "iac-b-%[1]s"
  source       = ligo_source.s.name
  target       = ligo_target.t.name
  access_level = "read"
  purpose      = "provider lab"
  parameters   = jsonencode({ connectionLimit = %[2]d })
}
`, suffix, limit)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg(5), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_binding.b", "phase", "ACTIVE"),
				resource.TestCheckResourceAttr("ligo_binding.b", "managed_by", owner),
				resource.TestCheckResourceAttr("ligo_binding.b", "revision", "1"),
			)},
			{Config: cfg(5), PlanOnly: true},
			{Config: cfg(9), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_binding.b", "phase", "ACTIVE"),
				resource.TestCheckResourceAttr("ligo_binding.b", "parameters", `{"connectionLimit":9}`),
			)},
			{ResourceName: "ligo_binding.b", ImportState: true, ImportStateId: "iac-b-" + suffix, ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore: []string{"managed", "last_change_request"}},
		},
	})
}

// TestLabInstances runs as an operator, who files what a person decides: an
// identity provider and a secret store of the plugin types that need no
// endpoint, each created only once lab.sh iac test approves its request as a
// person. The apply waits for the approval, files nothing twice, and the
// removal is approved the same way.
func TestLabInstances(t *testing.T) {
	suffix := labEnv(t, "LIGO_LAB_INSTANCE_SUFFIX")
	cfg := labProvider(t, "2m") + fmt.Sprintf(`
resource "ligo_identity_provider" "i" {
  name         = "iac-idp-%[1]s"
  jurisdiction = "dc/corp-prod"
  realm        = "prod"
  type         = "svid.v1"
}
resource "ligo_secret_store" "s" {
  name         = "iac-ss-%[1]s"
  jurisdiction = "dc/corp-prod"
  realm        = "prod"
  type         = "none.v1"
  description  = "provider lab"
}
`, suffix)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("ligo_identity_provider.i", "revision", "1"),
				resource.TestCheckResourceAttr("ligo_secret_store.s", "revision", "1"),
			)},
			{Config: cfg, PlanOnly: true},
			{ResourceName: "ligo_secret_store.s", ImportState: true, ImportStateId: "iac-ss-" + suffix, ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore: []string{"last_change_request"}},
		},
	})
}

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
				resource.TestMatchResourceAttr("data.ligo_catalog_targets.all", "items.#", regexp.MustCompile(`^[1-9]`)),
			),
		}},
	})
}

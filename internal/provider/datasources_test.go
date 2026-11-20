package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const pipeline = "spiffe://prod.example.org/ci/terraform"

// detail is a get answer: the item with the audit that the detail views add.
func detail(item string) string { return item[:len(item)-1] + `,"audit":[]}` }

func s(t *testing.T, st tfsdk.State, p path.Path) string {
	t.Helper()
	return attrOf[types.String](t, st, p).ValueString()
}

func TestSourceSingular(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/sources/ledger-app", 200, detail(sourceJSON("ledger-app", pipeline)))
	st, diags := read(t, "ligo_source", f.session(t), map[string]any{"name": "ledger-app"})
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	for k, want := range map[string]string{"name": "ledger-app", "trust_domain": "prod.example.org", "jurisdiction": "dc/corp-prod",
		"state": "ACTIVE", "managed_by": pipeline, "display_name": "Ledger", "visibility": "PUBLIC"} {
		if got := s(t, st, path.Root(k)); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if n := attrOf[types.Int64](t, st, path.Root("revision")).ValueInt64(); n != 3 {
		t.Errorf("revision %d", n)
	}
	if !attrOf[types.String](t, st, path.Root("description")).IsNull() {
		t.Error("an absent description is null")
	}
}

func TestSourceUnmanagedHasNoOwner(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/sources/s", 200, detail(sourceJSON("s", "")))
	st, diags := read(t, "ligo_source", f.session(t), map[string]any{"name": "s"})
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	if !attrOf[types.String](t, st, path.Root("managed_by")).IsNull() {
		t.Error("managed_by of an unmanaged source is null")
	}
}

func TestNotFound(t *testing.T) {
	f := newFake(t)
	_, diags := read(t, "ligo_source", f.session(t), map[string]any{"name": "nope"})
	if !diags.HasError() || !strings.Contains(errorText(diags), "ligo_source nope") || !strings.Contains(errorText(diags), "Not found") {
		t.Fatalf("diagnostics: %q", errorText(diags))
	}
}

// TestSourcesFiltersAndPages: the filters reach the server, and the pages are
// followed to the end.
func TestSourcesFiltersAndPages(t *testing.T) {
	f := newFake(t)
	f.routes["GET /v1/admin/sources"] = func(r *http.Request) (int, string) {
		if r.URL.Query().Get("pageToken") == "p2" {
			return 200, page(sourceJSON("b", ""), "")
		}
		return 200, page(sourceJSON("a", pipeline), "p2")
	}
	st, diags := read(t, "ligo_sources", f.session(t), map[string]any{"jurisdiction": "dc/*", "trust_domain": "prod.example.org", "state": "ACTIVE", "annotation": "team=fin", "q": "led"})
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	if a, b := s(t, st, path.Root("items").AtListIndex(0).AtName("name")), s(t, st, path.Root("items").AtListIndex(1).AtName("name")); a != "a" || b != "b" {
		t.Errorf("items %q %q", a, b)
	}
	seen := strings.Join(f.seen(), "\n")
	for _, q := range []string{"jurisdiction=dc%2F%2A", "trustDomain=prod.example.org", "state=ACTIVE", "annotation=team%3Dfin", "q=led", "pageToken=p2"} {
		if !strings.Contains(seen, q) {
			t.Errorf("no %s in the requests:\n%s", q, seen)
		}
	}
}

func TestLimitIsAnErrorNotATruncation(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/sources", 200, page(sourceJSON("a", "")+","+sourceJSON("b", ""), ""))
	_, diags := read(t, "ligo_sources", f.session(t), map[string]any{"limit": int64(1)})
	if !diags.HasError() || !strings.Contains(errorText(diags), "more than 1 items") {
		t.Fatalf("diagnostics: %q", errorText(diags))
	}
	for _, bad := range []int64{0, maxLimit + 1} {
		if _, diags := read(t, "ligo_sources", f.session(t), map[string]any{"limit": bad}); !diags.HasError() {
			t.Errorf("limit %d accepted", bad)
		}
	}
}

func TestTargetNormalisesItsJSON(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/targets/ledger-db", 200, detail(targetJSON("ledger-db")))
	st, diags := read(t, "ligo_target", f.session(t), map[string]any{"name": "ledger-db"})
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	if got := s(t, st, path.Root("definition")); got != `{"alpha":{"x":1,"y":2},"zeta":1}` {
		t.Errorf("definition %s", got)
	}
	if got := s(t, st, path.Root("access_levels")); got != `[{"grants":{"a":["x"],"b":1},"name":"read"}]` {
		t.Errorf("access_levels %s", got)
	}
	if got := s(t, st, path.Root("platform")); got != "pg-ledger" {
		t.Errorf("platform %s", got)
	}
}

func TestBindingsFiltersAndOwner(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/bindings", 200, page(bindingJSON("b1"), ""))
	st, diags := read(t, "ligo_bindings", f.session(t), map[string]any{"source": "ledger-app", "phase": "ACTIVE", "realm": "prod"})
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	it := path.Root("items").AtListIndex(0)
	if s(t, st, it.AtName("managed_by")) != pipeline || s(t, st, it.AtName("phase")) != "ACTIVE" || s(t, st, it.AtName("expires_at")) != "2027-01-01T00:00:00Z" {
		t.Errorf("binding %v", st.Raw)
	}
	if got := s(t, st, it.AtName("parameters")); got != `{"a":{"b":2,"z":1},"role":"r"}` {
		t.Errorf("parameters %s", got)
	}
	if seen := strings.Join(f.seen(), "\n"); !strings.Contains(seen, "source=ledger-app") || !strings.Contains(seen, "phase=ACTIVE") || !strings.Contains(seen, "realm=prod") {
		t.Errorf("requests:\n%s", seen)
	}
}

func TestBindingSingular(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/bindings/b1", 200, detail(bindingJSON("b1")))
	st, diags := read(t, "ligo_binding", f.session(t), map[string]any{"name": "b1"})
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	if s(t, st, path.Root("target")) != "ledger-db" || s(t, st, path.Root("access_level")) != "read" {
		t.Errorf("binding %v", st.Raw)
	}
}

func TestHolds(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/holds", 200, page(`{"id":"`+idA+`","bindingName":"b1","source":"s","target":"t","accessLevel":"read","basis":"CONTAINMENT","changeRequestId":"`+idB+`",
		"deletedAt":"2026-11-02T10:00:00Z","deletedBy":"olga","liftBasis":null,"liftComment":null,"liftedAt":null,"liftedBy":null,"managedBy":"`+pipeline+`",
		"platformJurisdiction":"dc/corp-prod","sourceJurisdiction":"dc/corp-prod","targetJurisdiction":"dc/corp-prod/ledger"}`, ""))
	st, diags := read(t, "ligo_holds", f.session(t), map[string]any{"binding": "b1", "all": true})
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	it := path.Root("items").AtListIndex(0)
	if s(t, st, it.AtName("binding")) != "b1" || s(t, st, it.AtName("basis")) != "CONTAINMENT" || s(t, st, it.AtName("id")) != idA {
		t.Errorf("hold %v", st.Raw)
	}
	if !attrOf[types.String](t, st, it.AtName("lifted_by")).IsNull() {
		t.Error("an open hold has no lifter")
	}
	if seen := strings.Join(f.seen(), "\n"); !strings.Contains(seen, "binding=b1") || !strings.Contains(seen, "state=all") {
		t.Errorf("requests:\n%s", seen)
	}
}

func TestCharter(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/charter", 200, `{"policy":{"b":1,"a":{"d":2,"c":3}},"sha256":"`+strings.Repeat("a", 64)+`"}`)
	st, diags := read(t, "ligo_charter", f.session(t), nil)
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	if s(t, st, path.Root("policy")) != `{"a":{"c":3,"d":2},"b":1}` || s(t, st, path.Root("sha256")) != strings.Repeat("a", 64) {
		t.Errorf("charter %v", st.Raw)
	}
}

func TestPlatforms(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/platforms", 200, `{"items":[{"apiVersion":"v","kind":"Platform","name":"pg-ledger","jurisdiction":"dc/corp-prod","realm":"prod","revision":2,
		"allowedSourceTrustDomains":["prod.example.org"],"identityProvider":{"instance":"svid-prod","type":"spiffe.v1"},"secretStore":{"instance":"vault-prod","type":"vault.v1"},
		"networkFlows":[],"targetJurisdictions":["dc/corp-prod/*"],"health":{"serving":true}}]}`)
	st, diags := read(t, "ligo_platforms", f.session(t), nil)
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	it := path.Root("items").AtListIndex(0)
	if s(t, st, it.AtName("name")) != "pg-ledger" || s(t, st, it.AtName("secret_store")) != "vault-prod" || !attrOf[types.Bool](t, st, it.AtName("serving")).ValueBool() {
		t.Errorf("platform %v", st.Raw)
	}
}

func TestCatalog(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/catalog/targets", 200, page(`{"name":"ledger-db","jurisdiction":"dc/corp-prod/ledger","platform":"pg-ledger","platformJurisdiction":"dc/corp-prod","platformType":"postgres.v1","state":"ACTIVE","bound":true}`, ""))
	st, diags := read(t, "ligo_catalog_targets", f.session(t), map[string]any{"for_source": "ledger-app", "include_deprecated": true})
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	if !attrOf[types.Bool](t, st, path.Root("items").AtListIndex(0).AtName("bound")).ValueBool() {
		t.Errorf("catalog %v", st.Raw)
	}
	if seen := strings.Join(f.seen(), "\n"); !strings.Contains(seen, "forSource=ledger-app") {
		t.Errorf("requests:\n%s", seen)
	}
	f.on("GET /v1/admin/catalog/targets/ledger-db", 200, strings.Replace(targetJSON("ledger-db"), `"status"`, `"outcomes":[{"source":"ledger-app","sourceJurisdiction":"dc/corp-prod","allowed":true,"sides":["TARGET"]}],"status"`, 1))
	st, diags = read(t, "ligo_catalog_target", f.session(t), map[string]any{"name": "ledger-db"})
	if diags.HasError() {
		t.Fatal(errorText(diags))
	}
	if got := s(t, st, path.Root("outcomes")); !strings.Contains(got, `"allowed":true`) {
		t.Errorf("outcomes %s", got)
	}
}

// TestErrorsCarryNoBody (T-P2): what a hostile or confused server puts in a
// body that is not a problem never reaches a diagnostic.
func TestErrorsCarryNoBody(t *testing.T) {
	f := newFake(t)
	f.on("GET /v1/admin/sources/x", 500, "TOPSECRET-BODY Authorization: Bearer abc")
	f.on("GET /v1/admin/charter", 200, `{"policy":`)
	for name, cfg := range map[string]map[string]any{"ligo_source": {"name": "x"}, "ligo_charter": nil} {
		_, diags := read(t, name, f.session(t), cfg)
		if !diags.HasError() {
			t.Fatalf("%s: no error", name)
		}
		if txt := errorText(diags); strings.Contains(txt, "TOPSECRET") || strings.Contains(txt, "Bearer") || strings.Contains(txt, testPassword) {
			t.Errorf("%s: a diagnostic carries what it must not: %q", name, txt)
		}
	}
}

func TestUnconfiguredProvider(t *testing.T) {
	_, diags := read(t, "ligo_charter", nil, nil)
	if !diags.HasError() || !strings.Contains(errorText(diags), "not configured") {
		t.Fatalf("diagnostics: %q", errorText(diags))
	}
}

func TestInstances(t *testing.T) {
	for _, tc := range []struct{ one, many, route, kind, typ string }{
		{"ligo_identity_provider", "ligo_identity_providers", "identity-providers", "IdentityProvider", "openldap.v1"},
		{"ligo_secret_store", "ligo_secret_stores", "secret-stores", "SecretStore", "vault.v1"},
	} {
		t.Run(tc.one, func(t *testing.T) {
			f := newFake(t)
			f.on("GET /v1/admin/"+tc.route+"/dir", 200, instanceJSON(tc.kind, "dir", tc.typ, true))
			f.on("GET /v1/admin/"+tc.route+"/plain", 200, instanceJSON(tc.kind, "plain", tc.typ, false))
			f.on("GET /v1/admin/"+tc.route, 200, `{"items":[`+instanceJSON(tc.kind, "a", tc.typ, true)+`,`+instanceJSON(tc.kind, "b", tc.typ, false)+`]}`)

			// who may file on the jurisdiction sees the revision and the config, in normal form
			st, diags := read(t, tc.one, f.session(t), map[string]any{"name": "dir"})
			if diags.HasError() {
				t.Fatal(errorText(diags))
			}
			if s(t, st, path.Root("type")) != tc.typ || s(t, st, path.Root("realm")) != "prod" || s(t, st, path.Root("jurisdiction")) != "dc/corp-prod" {
				t.Errorf("instance %v", st.Raw)
			}
			if n := attrOf[types.Int64](t, st, path.Root("revision")).ValueInt64(); n != 4 {
				t.Errorf("revision %d", n)
			}
			if got, want := s(t, st, path.Root("config")), `{"a":1,"base":"dc=example,dc=org","url":"https://ldap.example.org"}`; got != want {
				t.Errorf("config %s, want %s", got, want)
			}
			if !strings.Contains(s(t, st, path.Root("network_flows")), `"destination":"ldap.example.org"`) || !strings.Contains(s(t, st, path.Root("contacts")), `"ops"`) {
				t.Errorf("flows or contacts: %v", st.Raw)
			}

			// anyone else gets nulls, not zeros
			st, diags = read(t, tc.one, f.session(t), map[string]any{"name": "plain"})
			if diags.HasError() {
				t.Fatal(errorText(diags))
			}
			for _, k := range []string{"revision", "config"} {
				var v attr.Value
				if k == "revision" {
					v = attrOf[types.Int64](t, st, path.Root(k))
				} else {
					v = attrOf[types.String](t, st, path.Root(k))
				}
				if !v.IsNull() {
					t.Errorf("%s is %v, want null", k, v)
				}
			}

			// the plural keeps the order of ligo, by name
			st, diags = read(t, tc.many, f.session(t), nil)
			if diags.HasError() {
				t.Fatal(errorText(diags))
			}
			if s(t, st, path.Root("items").AtListIndex(0).AtName("name")) != "a" || s(t, st, path.Root("items").AtListIndex(1).AtName("name")) != "b" {
				t.Errorf("items out of order: %v", st.Raw)
			}
			if _, diags = read(t, tc.many, f.session(t), map[string]any{"limit": 1}); !strings.Contains(errorText(diags), "more than 1 items") {
				t.Errorf("limit: %q", errorText(diags))
			}
			if _, diags = read(t, tc.one, f.session(t), map[string]any{"name": "nope"}); !strings.Contains(errorText(diags), "Not found") {
				t.Errorf("not found: %q", errorText(diags))
			}
		})
	}
}

// T-P10: a config that carries a secret by mistake is not shown in a plan.
func TestInstanceConfigIsSensitive(t *testing.T) {
	for _, name := range []string{"ligo_identity_provider", "ligo_secret_store"} {
		d := datasourceNamed(t, name, nil)
		var r datasource.SchemaResponse
		d.Schema(context.Background(), datasource.SchemaRequest{}, &r)
		if a, ok := r.Schema.Attributes["config"].(dschema.StringAttribute); !ok || !a.Sensitive {
			t.Errorf("%s: config must be a sensitive string", name)
		}
		if a, ok := r.Schema.Attributes["type"].(dschema.StringAttribute); !ok || a.Sensitive {
			t.Errorf("%s: type must not be sensitive", name)
		}
	}
	for _, name := range []string{"ligo_identity_providers", "ligo_secret_stores"} {
		d := datasourceNamed(t, name, nil)
		var r datasource.SchemaResponse
		d.Schema(context.Background(), datasource.SchemaRequest{}, &r)
		items := r.Schema.Attributes["items"].(dschema.ListNestedAttribute)
		if a, ok := items.NestedObject.Attributes["config"].(dschema.StringAttribute); !ok || !a.Sensitive {
			t.Errorf("%s: items.config must be a sensitive string", name)
		}
	}
}

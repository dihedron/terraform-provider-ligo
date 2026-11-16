package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// the two kinds that share the model: tests run on both
var instanceKinds = map[string]func() *instanceResource{
	"identity provider": func() *instanceResource { return &instanceResource{kind: identityProviderKind} },
	"secret store":      func() *instanceResource { return &instanceResource{kind: secretStoreKind} },
}

func instRes(t *testing.T, mk func() *instanceResource, s *Session) (*instanceResource, resource.SchemaResponse) {
	t.Helper()
	r := mk()
	r.session = s
	var sch resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sch)
	return r, sch
}

func plannedInstance(name string) instanceModel {
	return instanceModel{
		ID: types.StringUnknown(), Name: types.StringValue(name), Jurisdiction: types.StringValue("dc/corp-prod"), Realm: types.StringValue("prod"), Type: types.StringValue("vault.v1"),
		DisplayName: types.StringNull(), Description: types.StringNull(), Contacts: types.ListNull(contactType), SvidSelector: types.ObjectNull(selectorType.AttrTypes),
		Config: types.StringValue(`{"address":"https://vault.example.org","role":"ligo"}`), NetworkFlows: types.StringNull(),
		Revision: types.Int64Unknown(), LastChangeRequest: types.StringUnknown(),
	}
}

func istate(t *testing.T, sch resource.SchemaResponse, m instanceModel) tfsdk.State {
	t.Helper()
	st := tfsdk.State{Schema: sch.Schema}
	if d := st.Set(context.Background(), m); d.HasError() {
		t.Fatal(errorText(d))
	}
	return st
}

func iplan(t *testing.T, sch resource.SchemaResponse, m instanceModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: sch.Schema}
	if d := p.Set(context.Background(), m); d.HasError() {
		t.Fatal(errorText(d))
	}
	return p
}

func createInstance(t *testing.T, r *instanceResource, sch resource.SchemaResponse, m instanceModel) (instanceModel, resource.CreateResponse) {
	t.Helper()
	resp := resource.CreateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: iplan(t, sch, m)}, &resp)
	var out instanceModel
	if !resp.State.Raw.IsNull() {
		resp.State.Get(context.Background(), &out)
	}
	return out, resp
}

func madeInstance(t *testing.T, r *instanceResource, sch resource.SchemaResponse, m instanceModel) instanceModel {
	t.Helper()
	got, resp := createInstance(t, r, sch, m)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	return got
}

func updateInstance(t *testing.T, r *instanceResource, sch resource.SchemaResponse, st, plan instanceModel) (instanceModel, resource.UpdateResponse) {
	t.Helper()
	resp := resource.UpdateResponse{State: istate(t, sch, st)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: iplan(t, sch, plan), State: istate(t, sch, st)}, &resp)
	var out instanceModel
	resp.State.Get(context.Background(), &out)
	return out, resp
}

func eachKind(t *testing.T, f func(t *testing.T, mk func() *instanceResource)) {
	for name, mk := range instanceKinds {
		t.Run(name, func(t *testing.T) { f(t, mk) })
	}
}

// A person decides every instance request: the apply waits for one, files once.
func TestInstanceCreateWaitsForAPerson(t *testing.T) {
	eachKind(t, func(t *testing.T, mk func() *instanceResource) {
		l := newLigoFake(t)
		l.approveIn = 200 * time.Millisecond
		r, sch := instRes(t, mk, ligoSession(t, l, 20*time.Second))
		m := plannedInstance("vault-prod")
		m.DisplayName = types.StringValue("Production")
		m.SvidSelector = types.ObjectValueMust(selectorType.AttrTypes, map[string]attr.Value{"match_expr": types.StringValue(`path == "/ligo/plugin/vault"`),
			"jwt_audience": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("https://vault.example.org")}), "allow_shared_svid": types.BoolValue(true)})
		m.Contacts = types.ListValueMust(contactType, []attr.Value{types.ObjectValueMust(contactType.AttrTypes, map[string]attr.Value{"name": types.StringValue("Platform team"), "email": types.StringNull()})})
		got := madeInstance(t, r, sch, m)
		if got.Revision.ValueInt64() != 1 || got.ID.ValueString() != "vault-prod" || got.LastChangeRequest.ValueString() == "" || got.DisplayName.ValueString() != "Production" {
			t.Errorf("state %+v", got)
		}
		if got.Config.ValueString() != `{"address":"https://vault.example.org","role":"ligo"}` {
			t.Errorf("config %q", got.Config.ValueString())
		}
		if got.SvidSelector.IsNull() || got.Contacts.IsNull() {
			t.Errorf("selector or contacts lost: %+v", got)
		}
		if l.count() != 1 {
			t.Errorf("%d filings, want 1", l.count())
		}
		body := l.crs[l.order[0]]["payload"].(map[string]any)
		if _, managed := body["managed"]; managed {
			t.Errorf("an instance is never managed: %v", body)
		}
		if body["kind"] != r.kind.defKind || body["metadata"].(map[string]any)["name"] != "vault-prod" {
			t.Errorf("the filing: %v", body)
		}
	})
}

// T-P6: a timeout leaves no state, and the next apply waits for the same request.
func TestInstanceRetryWaitsForTheOpenRequest(t *testing.T) {
	l := newLigoFake(t)
	r, sch := instRes(t, instanceKinds["secret store"], ligoSession(t, l, 1200*time.Millisecond))
	m := plannedInstance("vault-prod")
	_, resp := createInstance(t, r, sch, m)
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "still PENDING") || !strings.Contains(msg, "OWNER (dc/corp-prod)") || !resp.State.Raw.IsNull() {
		t.Fatalf("diagnostics: %q, state null: %v", msg, resp.State.Raw.IsNull())
	}
	l.approveIn = 0
	go func() { time.Sleep(1200 * time.Millisecond); l.decide(l.order[0], "APPLIED") }()
	r.session.ApprovalTimeout = 10 * time.Second
	got := madeInstance(t, r, sch, m)
	if l.count() != 1 || got.Revision.ValueInt64() != 1 {
		t.Errorf("%d filings, state %+v", l.count(), got)
	}
	// another content while one is pending is somebody else's change
	other := plannedInstance("vault-prod")
	other.Config = types.StringValue(`{"address":"https://other.example.org"}`)
	l.mu.Lock()
	delete(l.instances, "SECRET_STORE/vault-prod")
	l.mu.Unlock()
	r.session.ApprovalTimeout = time.Second
	createInstance(t, r, sch, m) // files and times out: pending
	if _, resp := createInstance(t, r, sch, other); !strings.Contains(errorText(resp.Diagnostics), "pending with another content") {
		t.Errorf("diagnostics: %q", errorText(resp.Diagnostics))
	}
}

func TestInstanceUpdateSendsTheRevisionAndStaleIsNotRetried(t *testing.T) {
	eachKind(t, func(t *testing.T, mk func() *instanceResource) {
		l := newLigoFake(t)
		l.approveIn = 100 * time.Millisecond
		r, sch := instRes(t, mk, ligoSession(t, l, 20*time.Second))
		st := madeInstance(t, r, sch, plannedInstance("vault-prod"))
		plan := st
		plan.Description, plan.Revision = types.StringValue("the vault"), types.Int64Unknown()
		got, resp := updateInstance(t, r, sch, st, plan)
		if resp.Diagnostics.HasError() {
			t.Fatal(errorText(resp.Diagnostics))
		}
		if got.Revision.ValueInt64() != 2 || got.Description.ValueString() != "the vault" {
			t.Errorf("state %+v", got)
		}
		if p := l.crs[l.order[1]]["payload"].(map[string]any); p["expectedRevision"] != float64(1) {
			t.Errorf("expectedRevision %v, want the state's 1", p["expectedRevision"])
		}
		l.mu.Lock()
		l.instances[r.kind.crKind+"/vault-prod"]["revision"] = int64(7)
		l.mu.Unlock()
		plan = got
		plan.Description = types.StringValue("again")
		before := l.count()
		_, resp = updateInstance(t, r, sch, got, plan)
		if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "changed outside Terraform") || l.count() != before {
			t.Errorf("stale: %q, %d filings", msg, l.count()-before)
		}
	})
}

func TestInstanceDeleteRetiresAndInUseIsExplained(t *testing.T) {
	eachKind(t, func(t *testing.T, mk func() *instanceResource) {
		l := newLigoFake(t)
		l.approveIn = 100 * time.Millisecond
		r, sch := instRes(t, mk, ligoSession(t, l, 20*time.Second))
		st := madeInstance(t, r, sch, plannedInstance("vault-prod"))
		l.inUse["vault-prod"] = true
		del := func() resource.DeleteResponse {
			resp := resource.DeleteResponse{State: istate(t, sch, st)}
			r.Delete(context.Background(), resource.DeleteRequest{State: istate(t, sch, st)}, &resp)
			return resp
		}
		if msg := errorText(del().Diagnostics); !strings.Contains(msg, "still used") || !strings.Contains(msg, "instance-in-use") {
			t.Fatalf("in use: %q", msg)
		}
		l.inUse["vault-prod"] = false
		if resp := del(); resp.Diagnostics.HasError() || l.instances[r.kind.crKind+"/vault-prod"] != nil {
			t.Errorf("retire: %s", errorText(resp.Diagnostics))
		}
		if op := l.crs[l.order[len(l.order)-1]]["operation"]; op != "DELETE" {
			t.Errorf("operation %v", op)
		}
	})
}

func TestInstanceReadGoneDriftViewerAndQuarantine(t *testing.T) {
	l := newLigoFake(t)
	l.approveIn = 100 * time.Millisecond
	r, sch := instRes(t, instanceKinds["identity provider"], ligoSession(t, l, 20*time.Second))
	st := madeInstance(t, r, sch, plannedInstance("ldap"))
	read := func() (instanceModel, resource.ReadResponse) {
		resp := resource.ReadResponse{State: istate(t, sch, st)}
		r.Read(context.Background(), resource.ReadRequest{State: istate(t, sch, st)}, &resp)
		var got instanceModel
		resp.State.Get(context.Background(), &got)
		return got, resp
	}
	key := "IDENTITY_PROVIDER/ldap"
	l.mu.Lock() // somebody changed it
	l.instances[key]["definition"].(map[string]any)["config"] = map[string]any{"address": "https://moved.example.org"}
	l.instances[key]["revision"] = int64(4)
	l.instances[key]["displayName"] = "By hand"
	l.mu.Unlock()
	got, resp := read()
	if got.Config.ValueString() != `{"address":"https://moved.example.org"}` || got.Revision.ValueInt64() != 4 || got.DisplayName.ValueString() != "By hand" || resp.Diagnostics.HasError() {
		t.Errorf("drift: %+v %v", got, errorText(resp.Diagnostics))
	}
	st = got
	l.mu.Lock()
	l.viewer = true
	l.mu.Unlock()
	got, resp = read()
	if got.Config.ValueString() != st.Config.ValueString() || got.Revision.ValueInt64() != 4 || resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("a viewer must not wipe the state: %+v %v", got, resp.Diagnostics)
	}
	l.mu.Lock()
	l.viewer = false
	l.quarantined[key] = true
	l.mu.Unlock()
	got, resp = read()
	if got.Revision.ValueInt64() != 4 || resp.Diagnostics.WarningsCount() != 1 || !strings.Contains(resp.Diagnostics.Warnings()[0].Summary(), "quarantined") {
		t.Errorf("quarantined: %+v %v", got.Revision, resp.Diagnostics)
	}
	l.mu.Lock()
	delete(l.instances, key)
	l.mu.Unlock()
	if _, resp := read(); resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Errorf("gone is removed: %s", errorText(resp.Diagnostics))
	}
}

func TestInstanceImport(t *testing.T) {
	r, sch := instRes(t, instanceKinds["secret store"], nil)
	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "vault-prod"}, &resp)
	var got instanceModel
	resp.State.Get(context.Background(), &got)
	if got.Name.ValueString() != "vault-prod" || got.ID.ValueString() != "vault-prod" {
		t.Errorf("import %+v", got)
	}
}

// T-P2, T-P10: the configuration is sensitive, the identity of an instance
// cannot change in place, and a refused config does not come back in the error.
func TestInstanceSchemaAndSecrets(t *testing.T) {
	eachKind(t, func(t *testing.T, mk func() *instanceResource) {
		l := newLigoFake(t)
		r, sch := instRes(t, mk, ligoSession(t, l, 5*time.Second))
		if !sch.Schema.Attributes["config"].IsSensitive() {
			t.Error("config must be sensitive")
		}
		for _, a := range []string{"name", "jurisdiction", "realm", "type"} {
			if len(sch.Schema.Attributes[a].(schema.StringAttribute).PlanModifiers) == 0 {
				t.Errorf("%s is immutable: it must replace", a)
			}
		}
		for name, a := range sch.Schema.Attributes {
			if strings.Contains(name, "password") || strings.Contains(name, "secret") || strings.Contains(name, "token") {
				t.Errorf("attribute %s could hold a credential", name)
			}
			_ = a
		}
		m := plannedInstance("vault-prod")
		m.Config = types.StringValue(`{"address":"x","password":"hunter2"}`)
		_, resp := createInstance(t, r, sch, m)
		msg := errorText(resp.Diagnostics)
		if !resp.Diagnostics.HasError() || strings.Contains(msg, "hunter2") || l.count() != 0 {
			t.Errorf("a refused config: %q, %d filings", msg, l.count())
		}
	})
}

func planInstance(t *testing.T, r *instanceResource, sch resource.SchemaResponse, st *instanceModel, m instanceModel) resource.ModifyPlanResponse {
	t.Helper()
	req := resource.ModifyPlanRequest{Plan: iplan(t, sch, m), State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	if st != nil {
		req.State = istate(t, sch, *st)
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.ModifyPlan(context.Background(), req, &resp)
	return resp
}

// An instance is never owned: a person may file it, and no owner is checked.
// A reformatting is no change, and a pending request is told.
func TestInstancePlan(t *testing.T) {
	l := newLigoFake(t)
	s := ligoSession(t, l, time.Second)
	s.Person = true
	r, sch := instRes(t, instanceKinds["secret store"], s)
	if resp := planInstance(t, r, sch, nil, plannedInstance("vault-prod")); resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 0 {
		t.Fatalf("a person files an instance: %v", resp.Diagnostics)
	}
	st := plannedInstance("vault-prod")
	st.ID, st.Revision, st.LastChangeRequest = types.StringValue("vault-prod"), types.Int64Value(1), types.StringValue("c")
	same := st
	same.Revision, same.LastChangeRequest = types.Int64Unknown(), types.StringUnknown()
	if resp := planInstance(t, r, sch, &st, same); resp.Diagnostics.HasError() || !resp.Plan.Raw.Equal(istate(t, sch, st).Raw) {
		t.Errorf("a reformatting is no change: %v", resp.Diagnostics)
	}
	createInstance(t, r, sch, plannedInstance("vault-prod")) // times out: pending
	resp := planInstance(t, r, sch, nil, plannedInstance("vault-prod"))
	if resp.Diagnostics.WarningsCount() != 1 || !strings.Contains(resp.Diagnostics.Warnings()[0].Detail(), "does not file another") {
		t.Errorf("pending: %v", resp.Diagnostics)
	}
}

func TestInstanceRateLimitedFilingIsRetried(t *testing.T) {
	fastRetry(t)
	l := newLigoFake(t)
	l.approveIn = 100 * time.Millisecond
	l.refuse = map[int]int{429: 1}
	r, sch := instRes(t, instanceKinds["identity provider"], ligoSession(t, l, 20*time.Second))
	madeInstance(t, r, sch, plannedInstance("ldap"))
	if l.count() != 1 {
		t.Errorf("%d filings", l.count())
	}
}

func TestInstanceConfigureRejectsOtherData(t *testing.T) {
	r := &instanceResource{kind: secretStoreKind}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: 3}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Error("a wrong type of provider data is an error")
	}
	r.Configure(context.Background(), resource.ConfigureRequest{}, &resp)
	var m resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "ligo"}, &m)
	if m.TypeName != "ligo_secret_store" {
		t.Errorf("type name %q", m.TypeName)
	}
}

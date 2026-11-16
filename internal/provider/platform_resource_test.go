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

func platRes(t *testing.T, s *Session) (*instanceResource, resource.SchemaResponse) {
	t.Helper()
	return instRes(t, func() *instanceResource { return &instanceResource{kind: platformKind} }, s)
}

func plannedPlatform(name string) platformModel {
	base := plannedInstance(name)
	base.Type, base.Config = types.StringValue("postgres.v1"), types.StringValue(`{"host":"pg.example.org","port":5432}`)
	return platformModel{
		instanceModel:             base,
		TargetJurisdictions:       types.ListUnknown(types.StringType),
		NamespaceTemplate:         types.StringNull(),
		CheckExistence:            types.BoolUnknown(),
		PrincipalPrefix:           types.StringNull(),
		AllowedSourceTrustDomains: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("prod.example.org")}),
		RotationInterval:          types.StringNull(),
		IdentityProvider:          types.ObjectValueMust(idpWiringType.AttrTypes, map[string]attr.Value{"type": types.StringValue("openldap.v1"), "instance": types.StringValue("ldap")}),
		SecretStore: types.ObjectValueMust(storeWiringType.AttrTypes, map[string]attr.Value{"type": types.StringValue("vault.v1"), "instance": types.StringValue("vault-prod"),
			"secret_storage_strategy": types.StringValue("vault-static")}),
	}
}

func pstate(t *testing.T, sch resource.SchemaResponse, m platformModel) tfsdk.State {
	t.Helper()
	st := tfsdk.State{Schema: sch.Schema}
	if d := st.Set(context.Background(), &m); d.HasError() {
		t.Fatal(errorText(d))
	}
	return st
}

func pplan(t *testing.T, sch resource.SchemaResponse, m platformModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: sch.Schema}
	if d := p.Set(context.Background(), &m); d.HasError() {
		t.Fatal(errorText(d))
	}
	return p
}

func createPlatform(t *testing.T, r *instanceResource, sch resource.SchemaResponse, m platformModel) (platformModel, resource.CreateResponse) {
	t.Helper()
	resp := resource.CreateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: pplan(t, sch, m)}, &resp)
	var out platformModel
	if !resp.State.Raw.IsNull() {
		resp.State.Get(context.Background(), &out)
	}
	return out, resp
}

func madePlatform(t *testing.T, r *instanceResource, sch resource.SchemaResponse, m platformModel) platformModel {
	t.Helper()
	got, resp := createPlatform(t, r, sch, m)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	return got
}

func TestPlatformCreateWaitsForAPersonAndReadsTheDefaults(t *testing.T) {
	l := newLigoFake(t)
	l.approveIn = 200 * time.Millisecond
	r, sch := platRes(t, ligoSession(t, l, 20*time.Second))
	m := plannedPlatform("pg-ledger")
	m.PrincipalPrefix = types.StringValue("svc_")
	got := madePlatform(t, r, sch, m)
	if got.Revision.ValueInt64() != 1 || got.PrincipalPrefix.ValueString() != "svc_" || !got.CheckExistence.ValueBool() {
		t.Errorf("state %+v", got)
	}
	if got.TargetJurisdictions.Elements()[0].(types.String).ValueString() != "dc/corp-prod/*" {
		t.Errorf("the default target jurisdictions: %v", got.TargetJurisdictions)
	}
	if v := got.SecretStore.Attributes()["secret_storage_strategy"].(types.String).ValueString(); v != "vault-static" {
		t.Errorf("secret store %v", got.SecretStore)
	}
	if got.IdentityProvider.Attributes()["instance"].(types.String).ValueString() != "ldap" || l.count() != 1 {
		t.Errorf("wiring %v, %d filings", got.IdentityProvider, l.count())
	}
	body := l.crs[l.order[0]]["payload"].(map[string]any)
	if body["kind"] != "Platform" || body["principalPrefix"] != "svc_" || body["identityProvider"].(map[string]any)["instance"] != "ldap" {
		t.Errorf("the filing: %v", body)
	}
	if _, managed := body["managed"]; managed {
		t.Errorf("a platform is never managed: %v", body)
	}
}

func TestPlatformUpdateStaleAndDelete(t *testing.T) {
	l := newLigoFake(t)
	l.approveIn = 100 * time.Millisecond
	r, sch := platRes(t, ligoSession(t, l, 20*time.Second))
	st := madePlatform(t, r, sch, plannedPlatform("pg-ledger"))
	plan := st
	plan.RotationInterval, plan.Revision = types.StringValue("24h"), types.Int64Unknown()
	resp := resource.UpdateResponse{State: pstate(t, sch, st)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: pplan(t, sch, plan), State: pstate(t, sch, st)}, &resp)
	var got platformModel
	resp.State.Get(context.Background(), &got)
	if resp.Diagnostics.HasError() || got.Revision.ValueInt64() != 2 || got.RotationInterval.ValueString() != "24h" {
		t.Fatalf("update: %s %+v", errorText(resp.Diagnostics), got)
	}
	if p := l.crs[l.order[1]]["payload"].(map[string]any); p["expectedRevision"] != float64(1) {
		t.Errorf("expectedRevision %v", p["expectedRevision"])
	}
	// somebody else changed it: stale, not retried
	l.mu.Lock()
	l.instances["PLATFORM/pg-ledger"]["revision"] = int64(9)
	l.mu.Unlock()
	before := l.count()
	plan = got
	plan.PrincipalPrefix = types.StringValue("x_")
	resp = resource.UpdateResponse{State: pstate(t, sch, got)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: pplan(t, sch, plan), State: pstate(t, sch, got)}, &resp)
	if !strings.Contains(errorText(resp.Diagnostics), "changed outside Terraform") || l.count() != before {
		t.Errorf("stale: %q", errorText(resp.Diagnostics))
	}
	// in use, then retired
	l.mu.Lock()
	l.instances["PLATFORM/pg-ledger"]["revision"] = int64(2)
	l.mu.Unlock()
	l.inUse["pg-ledger"] = true
	del := resource.DeleteResponse{State: pstate(t, sch, got)}
	r.Delete(context.Background(), resource.DeleteRequest{State: pstate(t, sch, got)}, &del)
	if !strings.Contains(errorText(del.Diagnostics), "still used") {
		t.Errorf("in use: %q", errorText(del.Diagnostics))
	}
	l.inUse["pg-ledger"] = false
	del = resource.DeleteResponse{State: pstate(t, sch, got)}
	r.Delete(context.Background(), resource.DeleteRequest{State: pstate(t, sch, got)}, &del)
	if del.Diagnostics.HasError() || l.instances["PLATFORM/pg-ledger"] != nil {
		t.Errorf("retire: %s", errorText(del.Diagnostics))
	}
}

func TestPlatformReadKeepsWhatAViewerCannotSee(t *testing.T) {
	l := newLigoFake(t)
	l.approveIn = 100 * time.Millisecond
	r, sch := platRes(t, ligoSession(t, l, 20*time.Second))
	st := madePlatform(t, r, sch, plannedPlatform("pg-ledger"))
	l.mu.Lock()
	l.viewer = true
	l.mu.Unlock()
	resp := resource.ReadResponse{State: pstate(t, sch, st)}
	r.Read(context.Background(), resource.ReadRequest{State: pstate(t, sch, st)}, &resp)
	var got platformModel
	resp.State.Get(context.Background(), &got)
	if resp.Diagnostics.WarningsCount() != 1 || got.IdentityProvider.IsNull() || got.Config.ValueString() != st.Config.ValueString() || got.Revision.ValueInt64() != 1 {
		t.Errorf("a viewer must not wipe the state: %v %+v", resp.Diagnostics, got)
	}
}

func TestPlatformSchema(t *testing.T) {
	r, sch := platRes(t, nil)
	for _, a := range []string{"identity_provider", "secret_store"} {
		if !sch.Schema.Attributes[a].IsRequired() {
			t.Errorf("%s is required: a platform without wiring is nothing", a)
		}
	}
	if !sch.Schema.Attributes["config"].IsSensitive() {
		t.Error("config must be sensitive")
	}
	for _, a := range []string{"name", "jurisdiction", "realm", "type"} {
		if len(sch.Schema.Attributes[a].(schema.StringAttribute).PlanModifiers) == 0 {
			t.Errorf("%s is immutable", a)
		}
	}
	var m resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "ligo"}, &m)
	if m.TypeName != "ligo_platform" {
		t.Errorf("type name %q", m.TypeName)
	}
}

func TestPlatformPlanReformattingIsNoChange(t *testing.T) {
	l := newLigoFake(t)
	l.approveIn = 100 * time.Millisecond
	r, sch := platRes(t, ligoSession(t, l, 20*time.Second))
	st := madePlatform(t, r, sch, plannedPlatform("pg-ledger"))
	same := st
	same.Revision, same.LastChangeRequest = types.Int64Unknown(), types.StringUnknown()
	req := resource.ModifyPlanRequest{Plan: pplan(t, sch, same), State: pstate(t, sch, st)}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.ModifyPlan(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() || !resp.Plan.Raw.Equal(pstate(t, sch, st).Raw) {
		t.Errorf("plan: %v", resp.Diagnostics)
	}
}

// A platform's plugin starts a moment after the approval: the read-back waits
// while ligo says it is unavailable, instead of losing a change that happened.
func TestPlatformReadBackWaitsForThePlugin(t *testing.T) {
	old, oldStep := readBackWait, readBackStep
	readBackWait, readBackStep = 5*time.Second, 50*time.Millisecond
	t.Cleanup(func() { readBackWait, readBackStep = old, oldStep })
	l := newLigoFake(t)
	l.approveIn = 100 * time.Millisecond
	r, sch := platRes(t, ligoSession(t, l, 20*time.Second))
	go func() { // from the first approval, the platform answers 503 for a while
		time.Sleep(50 * time.Millisecond)
		l.mu.Lock()
		l.unavailable = 4
		l.mu.Unlock()
	}()
	got := madePlatform(t, r, sch, plannedPlatform("pg-ledger"))
	if got.Revision.ValueInt64() != 1 {
		t.Errorf("state %+v", got)
	}
	readBackWait = 100 * time.Millisecond
	l.mu.Lock()
	l.unavailable = 1000
	l.mu.Unlock()
	if _, resp := createPlatform(t, r, sch, plannedPlatform("pg-two")); !strings.Contains(errorText(resp.Diagnostics), "Cannot read the platform") {
		t.Errorf("a platform that stays unavailable is an error: %q", errorText(resp.Diagnostics))
	}
}

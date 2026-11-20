package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func bndRes(t *testing.T, s *Session) (*bindingResource, resource.SchemaResponse) {
	t.Helper()
	bindingPoll = 10 * time.Millisecond
	r := &bindingResource{session: s}
	var sch resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sch)
	return r, sch
}

func plannedBinding(name string) bindingModel {
	return bindingModel{
		ID: types.StringUnknown(), Name: types.StringValue(name), Source: types.StringValue("ledger-app"), Target: types.StringValue("ledger"), AccessLevel: types.StringValue("read"),
		Parameters: types.StringValue(`{"connectionLimit":5}`), Description: types.StringNull(), ExpiresAt: types.StringNull(), Annotations: types.MapNull(types.StringType), Managed: types.BoolNull(),
		Revision: types.Int64Unknown(), ManagedBy: types.StringUnknown(), LastChangeRequest: types.StringUnknown(), Phase: types.StringUnknown(), PhaseMessage: types.StringUnknown(),
	}
}

func bstate(t *testing.T, sch resource.SchemaResponse, m bindingModel) tfsdk.State {
	t.Helper()
	st := tfsdk.State{Schema: sch.Schema}
	if d := st.Set(context.Background(), m); d.HasError() {
		t.Fatal(errorText(d))
	}
	return st
}

func bplan(t *testing.T, sch resource.SchemaResponse, m bindingModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: sch.Schema}
	if d := p.Set(context.Background(), m); d.HasError() {
		t.Fatal(errorText(d))
	}
	return p
}

func createBinding(t *testing.T, r *bindingResource, sch resource.SchemaResponse, m bindingModel) (bindingModel, resource.CreateResponse) {
	t.Helper()
	resp := resource.CreateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: bplan(t, sch, m)}, &resp)
	var out bindingModel
	if !resp.State.Raw.IsNull() {
		resp.State.Get(context.Background(), &out)
	}
	return out, resp
}

func madeBinding(t *testing.T, r *bindingResource, sch resource.SchemaResponse, m bindingModel) bindingModel {
	t.Helper()
	got, resp := createBinding(t, r, sch, m)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	return got
}

func updateBinding(t *testing.T, r *bindingResource, sch resource.SchemaResponse, st, plan bindingModel) (bindingModel, resource.UpdateResponse) {
	t.Helper()
	resp := resource.UpdateResponse{State: bstate(t, sch, st)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: bplan(t, sch, plan), State: bstate(t, sch, st)}, &resp)
	var out bindingModel
	resp.State.Get(context.Background(), &out)
	return out, resp
}

func TestBindingCreateWaitsUntilActive(t *testing.T) {
	l := newLigoFake(t)
	l.provisioning = 1
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	got := madeBinding(t, r, sch, plannedBinding("b"))
	if got.Phase.ValueString() != "ACTIVE" || got.Revision.ValueInt64() != 1 || got.ManagedBy.ValueString() != callerID || got.LastChangeRequest.ValueString() == "" {
		t.Errorf("state %+v", got)
	}
	if got.Parameters.ValueString() != `{"connectionLimit":5}` {
		t.Errorf("parameters %q", got.Parameters.ValueString())
	}
	if l.count() != 1 {
		t.Errorf("%d filings, want 1", l.count())
	}
	body := l.crs[l.order[0]]["payload"].(map[string]any)
	if body["managed"] != true || body["source"] != "ledger-app" || body["accessLevel"] != "read" {
		t.Errorf("the filing: %v", body)
	}
}

func TestBindingCreateFailedIsInTheStateAndAnError(t *testing.T) {
	l := newLigoFake(t)
	l.failing = true
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	got, resp := createBinding(t, r, sch, plannedBinding("b"))
	msg := errorText(resp.Diagnostics)
	if !strings.Contains(msg, "did not come up") || !strings.Contains(msg, "no such role") || !strings.Contains(msg, "-replace") {
		t.Fatalf("diagnostics: %q", msg)
	}
	if got.Phase.ValueString() != "FAILED" || got.Name.ValueString() != "b" {
		t.Errorf("the binding exists in ligo, so the state holds it (Terraform taints it): %+v", got)
	}
}

func TestBindingCreatePendingTimesOutWithoutState(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = false
	r, sch := bndRes(t, ligoSession(t, l, 1500*time.Millisecond))
	_, resp := createBinding(t, r, sch, plannedBinding("b"))
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "still PENDING") || !strings.Contains(msg, "TARGET (dc/corp-prod/ledger)") {
		t.Fatalf("diagnostics: %q", msg)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("a change that did not happen must not be in the state")
	}
}

// T-P6: the apply after a timeout waits for the open request.
func TestBindingRetryWaitsForTheOpenRequest(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = false
	r, sch := bndRes(t, ligoSession(t, l, 1500*time.Millisecond))
	m := plannedBinding("b")
	if _, resp := createBinding(t, r, sch, m); !resp.Diagnostics.HasError() {
		t.Fatal("expected a timeout")
	}
	go func() { time.Sleep(1200 * time.Millisecond); l.decide(l.order[0], "APPLIED") }()
	r.session.ApprovalTimeout = 10 * time.Second
	got := madeBinding(t, r, sch, m)
	if l.count() != 1 || got.Revision.ValueInt64() != 1 {
		t.Errorf("%d filings, state %+v", l.count(), got)
	}
	if _, resp := createBinding(t, r, sch, plannedBinding("b")); resp.Diagnostics.HasError() {
		t.Logf("a second create of an existing binding is ligo's to refuse: %s", errorText(resp.Diagnostics))
	}
}

func TestBindingUpdateSendsTheRevisionAndWaits(t *testing.T) {
	l := newLigoFake(t)
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	st := madeBinding(t, r, sch, plannedBinding("b"))
	plan := st
	plan.Parameters, plan.Description = types.StringValue(`{"connectionLimit":9}`), types.StringValue("reporting")
	plan.Revision, plan.Phase, plan.PhaseMessage = types.Int64Unknown(), types.StringUnknown(), types.StringUnknown()
	got, resp := updateBinding(t, r, sch, st, plan)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if got.Revision.ValueInt64() != 2 || got.Phase.ValueString() != "ACTIVE" || got.Description.ValueString() != "reporting" || got.Parameters.ValueString() != `{"connectionLimit":9}` {
		t.Errorf("state %+v", got)
	}
	if p := l.crs[l.order[1]]["payload"].(map[string]any); p["expectedRevision"] != float64(1) {
		t.Errorf("expectedRevision %v, want the state's 1", p["expectedRevision"])
	}
}

func TestBindingStaleRevisionIsNotRetried(t *testing.T) {
	l := newLigoFake(t)
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	st := madeBinding(t, r, sch, plannedBinding("b"))
	l.mu.Lock()
	l.bindings["b"]["metadata"].(map[string]any)["revision"] = int64(7)
	l.mu.Unlock()
	plan := st
	plan.Description = types.StringValue("x")
	_, resp := updateBinding(t, r, sch, st, plan)
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "changed outside Terraform") {
		t.Fatalf("diagnostics: %q", msg)
	}
	if l.count() != 1 {
		t.Errorf("%d filings: a stale revision must not be retried", l.count())
	}
}

func TestBindingUpdateFailedKeepsWhatLigoHolds(t *testing.T) {
	l := newLigoFake(t)
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	st := madeBinding(t, r, sch, plannedBinding("b"))
	l.failing = true
	plan := st
	plan.Parameters = types.StringValue(`{"connectionLimit":9}`)
	got, resp := updateBinding(t, r, sch, st, plan)
	if !strings.Contains(errorText(resp.Diagnostics), "did not come up") || got.Phase.ValueString() != "FAILED" || got.Revision.ValueInt64() != 2 {
		t.Errorf("%q %+v", errorText(resp.Diagnostics), got)
	}
}

// A hold and a break-glass review stop the apply, say who may act, and are not
// worked around: nothing is retried and nothing lifts the hold (T-P14).
func TestBindingHoldAndBreakGlassAreNamed(t *testing.T) {
	l := newLigoFake(t)
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	l.held["b"] = true
	_, resp := createBinding(t, r, sch, plannedBinding("b"))
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "ligo hold lift") || !strings.Contains(msg, "never lifts") {
		t.Fatalf("hold: %q", msg)
	}
	if l.count() != 0 {
		t.Errorf("%d filings", l.count())
	}
	l.held["b"] = false
	st := madeBinding(t, r, sch, plannedBinding("b"))
	l.breakGlass["b"] = "0194b7a0-1111-7000-8000-000000000001"
	plan := st
	plan.Description = types.StringValue("x")
	_, up := updateBinding(t, r, sch, st, plan)
	if msg := errorText(up.Diagnostics); !strings.Contains(msg, "break-glass") || !strings.Contains(msg, "0194b7a0-1111") || !strings.Contains(msg, "reviewed") {
		t.Fatalf("break-glass: %q", msg)
	}
	for _, seen := range l.seen() {
		if strings.Contains(seen, "/holds/") {
			t.Errorf("the provider called %s", seen)
		}
	}
}

func TestBindingDeleteRevokesAndWaitsUntilGone(t *testing.T) {
	l := newLigoFake(t)
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	st := madeBinding(t, r, sch, plannedBinding("b"))
	resp := resource.DeleteResponse{State: bstate(t, sch, st)}
	r.Delete(context.Background(), resource.DeleteRequest{State: bstate(t, sch, st)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if op := l.crs[l.order[len(l.order)-1]]["operation"]; op != "DELETE" || l.bindings["b"] != nil {
		t.Errorf("operation %v, binding %v", op, l.bindings["b"])
	}
}

func TestBindingDeleteFailedAndStuck(t *testing.T) {
	l := newLigoFake(t)
	r, sch := bndRes(t, ligoSession(t, l, 1500*time.Millisecond))
	st := madeBinding(t, r, sch, plannedBinding("b"))
	// the revocation is applied but the teardown does not finish
	l.on("DELETE /v1/admin/bindings/b", 202, `{"changeRequest":{"id":"0194b7a0-0000-7000-8000-0000000000aa","resourceType":"BINDING","resourceName":"b","operation":"DELETE","status":"APPLIED","kind":"STANDARD","consent":"ALL","policySha256":"a","payloadSha256":"b","requestedBy":"c","requestedAt":"2026-11-14T10:00:00Z","expiresAt":"2026-11-15T10:00:00Z","dependsOn":null,"resourceId":null,"awaitingApproval":[],"sides":[],"distinctApproversRequired":false},"resource":null}`)
	l.on("GET /v1/admin/change-requests/0194b7a0-0000-7000-8000-0000000000aa", 200, `{"id":"0194b7a0-0000-7000-8000-0000000000aa","resourceType":"BINDING","resourceName":"b","operation":"DELETE","status":"APPLIED","kind":"STANDARD","consent":"ALL","policySha256":"a","payloadSha256":"b","requestedBy":"c","requestedAt":"2026-11-14T10:00:00Z","expiresAt":"2026-11-15T10:00:00Z","dependsOn":null,"resourceId":null,"awaitingApproval":[],"sides":[],"distinctApproversRequired":false}`)
	run := func() string {
		resp := resource.DeleteResponse{State: bstate(t, sch, st)}
		r.Delete(context.Background(), resource.DeleteRequest{State: bstate(t, sch, st)}, &resp)
		return errorText(resp.Diagnostics)
	}
	l.mu.Lock()
	l.bindings["b"]["status"] = map[string]any{"phase": "DELETING", "message": ""}
	l.mu.Unlock()
	if msg := run(); !strings.Contains(msg, "still being revoked") {
		t.Errorf("stuck: %q", msg)
	}
	l.mu.Lock()
	l.bindings["b"]["status"] = map[string]any{"phase": "DELETE_FAILED", "message": "the role is in use"}
	l.mu.Unlock()
	if msg := run(); !strings.Contains(msg, "DELETE_FAILED") || !strings.Contains(msg, "the role is in use") {
		t.Errorf("failed: %q", msg)
	}
}

func TestBindingReadRemovesWhatIsGoneAndShowsDrift(t *testing.T) {
	l := newLigoFake(t)
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	st := madeBinding(t, r, sch, plannedBinding("b"))
	read := func() (bindingModel, resource.ReadResponse) {
		resp := resource.ReadResponse{State: bstate(t, sch, st)}
		r.Read(context.Background(), resource.ReadRequest{State: bstate(t, sch, st)}, &resp)
		var got bindingModel
		resp.State.Get(context.Background(), &got)
		return got, resp
	}
	l.mu.Lock()
	l.bindings["b"]["parameters"] = map[string]any{"connectionLimit": float64(50)}
	l.bindings["b"]["metadata"].(map[string]any)["revision"] = int64(9)
	l.bindings["b"]["metadata"].(map[string]any)["managedBy"] = nil
	l.bindings["b"]["status"] = map[string]any{"phase": "ACTIVE", "message": "", "breakGlass": map[string]any{
		"changeRequestId": "0194b7a0-1111-7000-8000-000000000001", "expiresAt": "2026-11-20T10:00:00Z", "incidentReference": "INC-1", "requestedBy": "dave"}}
	l.mu.Unlock()
	got, resp := read()
	if got.Parameters.ValueString() != `{"connectionLimit":50}` || got.Revision.ValueInt64() != 9 || !got.ManagedBy.IsNull() {
		t.Errorf("drift: %+v", got)
	}
	if resp.Diagnostics.WarningsCount() != 1 || !strings.Contains(resp.Diagnostics.Warnings()[0].Detail(), "INC-1") {
		t.Errorf("a break-glass change is told: %v", resp.Diagnostics)
	}
	l.mu.Lock()
	l.bindings["b"]["status"] = map[string]any{"phase": "FAILED", "message": "no such role"}
	l.mu.Unlock()
	if got, resp := read(); got.Phase.ValueString() != "FAILED" || resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("a failed binding is shown and told: %+v %v", got.Phase, resp.Diagnostics)
	}
	l.mu.Lock()
	delete(l.bindings, "b")
	l.mu.Unlock()
	if _, resp := read(); resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Errorf("gone is removed: %v", errorText(resp.Diagnostics))
	}
}

func TestBindingImport(t *testing.T) {
	r, sch := bndRes(t, nil)
	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "b"}, &resp)
	var got bindingModel
	resp.State.Get(context.Background(), &got)
	if got.Name.ValueString() != "b" || got.ID.ValueString() != "b" {
		t.Errorf("import %+v", got)
	}
}

func TestBindingAllAttributesRoundTrip(t *testing.T) {
	l := newLigoFake(t)
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	m := plannedBinding("b")
	m.Description, m.ExpiresAt = types.StringValue("reporting"), types.StringValue("2027-01-02T03:04:05+01:00")
	m.Annotations = types.MapValueMust(types.StringType, map[string]attr.Value{"team": types.StringValue("fin")})
	m.Managed = types.BoolValue(false)
	got := madeBinding(t, r, sch, m)
	if got.Description.ValueString() != "reporting" || got.ExpiresAt.ValueString() != "2027-01-02T03:04:05+01:00" || got.Annotations.Elements()["team"].(types.String).ValueString() != "fin" || !got.ManagedBy.IsNull() {
		t.Errorf("state %+v", got)
	}
}

func planBinding(t *testing.T, r *bindingResource, sch resource.SchemaResponse, st *bindingModel, m bindingModel) resource.ModifyPlanResponse {
	t.Helper()
	req := resource.ModifyPlanRequest{Plan: bplan(t, sch, m), State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	if st != nil {
		req.State = bstate(t, sch, *st)
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.ModifyPlan(context.Background(), req, &resp)
	return resp
}

const bindingFormsJSON = `{"bindingParameters":{"fields":[{"jsonKey":"connectionLimit","fieldType":"UI_FIELD_TYPE_NUMBER","required":false}]}}`

func TestBindingPlanValidatesParameters(t *testing.T) {
	l := newLigoFake(t)
	l.on("GET /v1/admin/targets/ledger", 200, `{"kind":"Target","metadata":{"name":"ledger","platform":"pg","revision":1,"state":"ACTIVE"},"definition":{},"accessLevels":[]}`)
	l.on("GET /v1/admin/platforms/pg", 200, `{"apiVersion":"v","kind":"Platform","name":"pg","jurisdiction":"dc/corp-prod","realm":"prod","identityProvider":{"instance":"i","type":"t"},
		"secretStore":{"instance":"s","type":"t"},"allowedSourceTrustDomains":[],"networkFlows":[],"uiSchemas":`+bindingFormsJSON+`}`)
	r, sch := bndRes(t, ligoSession(t, l, time.Second))
	if resp := planBinding(t, r, sch, nil, plannedBinding("b")); resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 0 {
		t.Fatalf("valid parameters: %v", resp.Diagnostics)
	}
	bad := plannedBinding("b")
	bad.Parameters = types.StringValue(`{"connectionLimit":"many","password":"hunter2"}`)
	if msg := errorText(planBinding(t, r, sch, nil, bad).Diagnostics); !strings.Contains(msg, "password is not a field") {
		t.Fatalf("diagnostics: %q", msg)
	}
	notObject := plannedBinding("b")
	notObject.Parameters = types.StringValue(`[1]`)
	if msg := errorText(planBinding(t, r, sch, nil, notObject).Diagnostics); !strings.Contains(msg, "Not a JSON object") {
		t.Fatalf("diagnostics: %q", msg)
	}
	// a platform that cannot be read: a warning; a target that does not exist yet: nothing to check
	l.on("GET /v1/admin/platforms/pg", 500, `oops`)
	if resp := planBinding(t, r, sch, nil, plannedBinding("b")); resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("unreadable platform: %v", resp.Diagnostics)
	}
	unknown := plannedBinding("b")
	unknown.Target = types.StringUnknown()
	if resp := planBinding(t, r, sch, nil, unknown); resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("a target made by this plan: %v", resp.Diagnostics)
	}
	l.on("GET /v1/admin/targets/ledger", 404, `{"type":"urn:ligo:problem:not-found","title":"nf","status":404}`)
	if resp := planBinding(t, r, sch, nil, plannedBinding("b")); resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("a target that does not exist yet: %v", resp.Diagnostics)
	}
}

func TestBindingPlanLockChecks(t *testing.T) {
	l := newLigoFake(t)
	s := ligoSession(t, l, time.Second)
	s.Person = true
	r, sch := bndRes(t, s)
	if msg := errorText(planBinding(t, r, sch, nil, plannedBinding("b")).Diagnostics); !strings.Contains(msg, "A person cannot own a resource") {
		t.Fatalf("diagnostics: %q", msg)
	}
	r2, sch2 := bndRes(t, ligoSession(t, l, time.Second))
	st := plannedBinding("b")
	st.ID, st.Revision, st.Phase, st.PhaseMessage = types.StringValue("b"), types.Int64Value(1), types.StringValue("ACTIVE"), types.StringValue("")
	st.ManagedBy, st.LastChangeRequest = types.StringValue("spiffe://prod.example.org/ci/other"), types.StringValue("c")
	plan := st
	plan.Description = types.StringValue("changed")
	if msg := errorText(planBinding(t, r2, sch2, &st, plan).Diagnostics); !strings.Contains(msg, "Managed by another automation") {
		t.Fatalf("diagnostics: %q", msg)
	}
	// only reformatted parameters (the attribute's plan modifier has put the state's text back, and the framework marked what ligo computes as unknown):
	// nothing to file, nothing to say
	st.ManagedBy = types.StringValue(callerID)
	same := st
	same.Revision, same.Phase, same.PhaseMessage, same.LastChangeRequest = types.Int64Unknown(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
	resp := planBinding(t, r2, sch2, &st, same)
	if resp.Diagnostics.HasError() || !resp.Plan.Raw.Equal(bstate(t, sch2, st).Raw) {
		t.Errorf("a reformatting is no change: %v", resp.Diagnostics)
	}
}

func TestBindingPlanWarnsOfAPendingRequest(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = false
	r, sch := bndRes(t, ligoSession(t, l, time.Second))
	m := plannedBinding("b")
	createBinding(t, r, sch, m)
	resp := planBinding(t, r, sch, nil, m)
	if resp.Diagnostics.WarningsCount() != 1 || !strings.Contains(resp.Diagnostics.Warnings()[0].Detail(), "does not file another") {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
}

func TestBindingRateLimitedFilingIsRetried(t *testing.T) {
	fastRetry(t)
	l := newLigoFake(t)
	l.refuse = map[int]int{429: 1}
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	madeBinding(t, r, sch, plannedBinding("b"))
	if l.count() != 1 {
		t.Errorf("%d filings", l.count())
	}
}

func TestBindingConfigureRejectsOtherData(t *testing.T) {
	r := &bindingResource{}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: 3}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Error("a wrong type of provider data is an error")
	}
	r.Configure(context.Background(), resource.ConfigureRequest{}, &resp)
}

// A binding without parameters in the configuration gets the platform's
// defaults from ligo: the attribute is computed, or Terraform refuses the
// result as inconsistent (found by the pre-release campaign on the real ligo).
func TestBindingParametersAreComputed(t *testing.T) {
	_, sch := bndRes(t, &Session{})
	a, ok := sch.Schema.Attributes["parameters"]
	if !ok || !a.IsOptional() || !a.IsComputed() {
		t.Fatalf("parameters: optional %v, computed %v; want both", a.IsOptional(), a.IsComputed())
	}
}

func TestBindingWithoutParametersKeepsTheServersDefaults(t *testing.T) {
	l := newLigoFake(t)
	r, sch := bndRes(t, ligoSession(t, l, 20*time.Second))
	plan := plannedBinding("b-defaults")
	plan.Parameters = types.StringUnknown()
	got, resp := createBinding(t, r, sch, plan)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if got.Parameters.ValueString() != `{"connectionLimit":0}` {
		t.Errorf("parameters %v, want the platform's default", got.Parameters)
	}
}

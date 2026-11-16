package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func tgtRes(t *testing.T, s *Session) (*targetResource, resource.SchemaResponse) {
	t.Helper()
	r := &targetResource{session: s}
	var sch resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sch)
	return r, sch
}

func levelsOf(grants map[string]string) types.Map {
	out := map[string]attr.Value{}
	for n, g := range grants {
		out[n] = types.ObjectValueMust(levelType.AttrTypes, map[string]attr.Value{"grants": types.StringValue(g), "description": types.StringNull(), "state": types.StringUnknown()})
	}
	return types.MapValueMust(levelType, out)
}

// plannedTarget is the plan of a target that the configuration describes.
func plannedTarget(name string) targetModel {
	return targetModel{
		ID: types.StringUnknown(), Name: types.StringValue(name), Platform: types.StringValue("pg"), DisplayName: types.StringNull(), Description: types.StringNull(),
		DocumentationURL: types.StringNull(), Definition: types.StringValue(`{"schema":"ledger","database":"ledger"}`),
		AccessLevels: levelsOf(map[string]string{"read": `{"roles":["RO"]}`}), NetworkFlows: types.StringNull(), Labels: types.MapNull(types.StringType),
		Visibility: types.StringUnknown(), VisibleTo: types.ListNull(types.StringType), State: types.StringUnknown(), Managed: types.BoolNull(),
		Jurisdiction: types.StringValue("dc/corp-prod/ledger"), Revision: types.Int64Unknown(), ManagedBy: types.StringUnknown(), LastChangeRequest: types.StringUnknown(),
	}
}

func createTarget(t *testing.T, r *targetResource, sch resource.SchemaResponse, m targetModel) (targetModel, resource.CreateResponse) {
	t.Helper()
	plan := tfsdk.Plan{Schema: sch.Schema}
	if d := plan.Set(context.Background(), m); d.HasError() {
		t.Fatal(errorText(d))
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	var out targetModel
	if !resp.State.Raw.IsNull() {
		resp.State.Get(context.Background(), &out)
	}
	return out, resp
}

func madeTarget(t *testing.T, r *targetResource, sch resource.SchemaResponse, m targetModel) targetModel {
	t.Helper()
	got, resp := createTarget(t, r, sch, m)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	return got
}

func updateTarget(t *testing.T, r *targetResource, sch resource.SchemaResponse, st, plan targetModel) (targetModel, resource.UpdateResponse) {
	t.Helper()
	mk := func(m targetModel) tfsdk.State {
		s := tfsdk.State{Schema: sch.Schema}
		if d := s.Set(context.Background(), m); d.HasError() {
			t.Fatal(errorText(d))
		}
		return s
	}
	p := tfsdk.Plan{Schema: sch.Schema}
	if d := p.Set(context.Background(), plan); d.HasError() {
		t.Fatal(errorText(d))
	}
	resp := resource.UpdateResponse{State: mk(st)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: p, State: mk(st)}, &resp)
	var out targetModel
	if !resp.Diagnostics.HasError() {
		resp.State.Get(context.Background(), &out)
	}
	return out, resp
}

func lastOp(l *ligoFake) (op, level string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cr := l.crs[l.order[len(l.order)-1]]
	return cr["operation"].(string), cr["level"].(string)
}

func TestTargetCreateApplied(t *testing.T) {
	l := newLigoFake(t)
	r, sch := tgtRes(t, ligoSession(t, l, 5*time.Second))
	got := madeTarget(t, r, sch, plannedTarget("ledger-db"))
	if got.Revision.ValueInt64() != 1 || got.ManagedBy.ValueString() != callerID || got.State.ValueString() != "ACTIVE" || got.Platform.ValueString() != "pg" {
		t.Errorf("state %+v", got)
	}
	// the user's text is kept while it means the same as ligo's
	if got.Definition.ValueString() != `{"schema":"ledger","database":"ledger"}` || got.levels()["read"].Grants.ValueString() != `{"roles":["RO"]}` {
		t.Errorf("definition %q, levels %v", got.Definition.ValueString(), got.AccessLevels)
	}
	if got.levels()["read"].State.ValueString() != "" && got.levels()["read"].State.IsUnknown() {
		t.Error("the state of a level is known after the apply")
	}
	body := l.crs[l.order[0]]["payload"].(map[string]any)
	if body["managed"] != true || body["metadata"].(map[string]any)["platform"] != "pg" || len(body["accessLevels"].([]any)) != 1 {
		t.Errorf("filing %v", body)
	}
	if l.count() != 1 {
		t.Errorf("%d filings", l.count())
	}
}

func TestTargetPendingTimesOutWithoutState(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = false
	r, sch := tgtRes(t, ligoSession(t, l, 1500*time.Millisecond))
	_, resp := createTarget(t, r, sch, plannedTarget("t"))
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "still PENDING") || !resp.State.Raw.IsNull() {
		t.Fatalf("diagnostics: %q, state null: %v", msg, resp.State.Raw.IsNull())
	}
	// the same plan again waits for the open request, and files nothing
	go func() { time.Sleep(1200 * time.Millisecond); l.decide(l.order[0], "APPLIED") }()
	r.session.ApprovalTimeout = 8 * time.Second
	if _, resp := createTarget(t, r, sch, plannedTarget("t")); resp.Diagnostics.HasError() || l.count() != 1 {
		t.Fatalf("%s, %d filings", errorText(resp.Diagnostics), l.count())
	}
}

func TestTargetLevels(t *testing.T) {
	l := newLigoFake(t)
	r, sch := tgtRes(t, ligoSession(t, l, 5*time.Second))
	st := madeTarget(t, r, sch, plannedTarget("t"))

	// a level added goes in the update
	plan := st
	plan.AccessLevels = levelsOf(map[string]string{"read": `{"roles":["RO"]}`, "write": `{"roles":["RW"]}`})
	plan.Revision = types.Int64Unknown()
	got, resp := updateTarget(t, r, sch, st, plan)
	if resp.Diagnostics.HasError() || len(got.levels()) != 2 || l.count() != 2 {
		t.Fatalf("add: %s %v, %d filings", errorText(resp.Diagnostics), got.AccessLevels, l.count())
	}
	if op, _ := lastOp(l); op != "UPDATE" {
		t.Errorf("operation %s", op)
	}

	// a level deprecated, then reactivated: one request each, nothing else
	st = got
	plan = st
	plan.AccessLevels = types.MapValueMust(levelType, map[string]attr.Value{
		"read":  st.levels()["read"].obj(),
		"write": levelModel{Grants: st.levels()["write"].Grants, Description: types.StringNull(), State: types.StringValue("DEPRECATED")}.obj(),
	})
	got, resp = updateTarget(t, r, sch, st, plan)
	if op, level := lastOp(l); resp.Diagnostics.HasError() || op != "DEPRECATE_LEVEL" || level != "write" || l.count() != 3 {
		t.Fatalf("deprecate: %s %s %s, %d filings", errorText(resp.Diagnostics), op, level, l.count())
	}
	if got.levels()["write"].State.ValueString() != "DEPRECATED" {
		t.Errorf("levels %v", got.AccessLevels)
	}

	// a level removed from the map is retired
	st = got
	plan = st
	plan.AccessLevels = types.MapValueMust(levelType, map[string]attr.Value{"read": st.levels()["read"].obj()})
	got, resp = updateTarget(t, r, sch, st, plan)
	if op, level := lastOp(l); resp.Diagnostics.HasError() || op != "RETIRE_LEVEL" || level != "write" {
		t.Fatalf("retire: %s %s %s", errorText(resp.Diagnostics), op, level)
	}
	if _, still := got.levels()["write"]; still || len(got.levels()) != 1 {
		t.Errorf("a retired level has left the target: %v", got.AccessLevels)
	}
}

func TestTargetStateAndNoOp(t *testing.T) {
	l := newLigoFake(t)
	r, sch := tgtRes(t, ligoSession(t, l, 5*time.Second))
	st := madeTarget(t, r, sch, plannedTarget("t"))
	plan := st
	plan.State = types.StringValue("DEPRECATED")
	got, resp := updateTarget(t, r, sch, st, plan)
	if op, _ := lastOp(l); resp.Diagnostics.HasError() || op != "DEPRECATE" || got.State.ValueString() != "DEPRECATED" || l.count() != 2 {
		t.Fatalf("%s %s %v, %d filings (a state change alone files no update)", errorText(resp.Diagnostics), op, got.State, l.count())
	}
	if _, resp := updateTarget(t, r, sch, got, got); resp.Diagnostics.HasError() || l.count() != 2 {
		t.Errorf("an update with no change files nothing: %d filings", l.count())
	}
}

func TestTargetStaleRevisionIsNotRetried(t *testing.T) {
	l := newLigoFake(t)
	r, sch := tgtRes(t, ligoSession(t, l, 5*time.Second))
	st := madeTarget(t, r, sch, plannedTarget("t"))
	l.mu.Lock()
	l.targets["t"]["metadata"].(map[string]any)["revision"] = int64(7)
	l.mu.Unlock()
	plan := st
	plan.Description = types.StringValue("x")
	_, resp := updateTarget(t, r, sch, st, plan)
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "changed outside Terraform") || l.count() != 1 {
		t.Fatalf("diagnostics %q, %d filings", msg, l.count())
	}
}

func TestTargetReadDeleteImport(t *testing.T) {
	l := newLigoFake(t)
	r, sch := tgtRes(t, ligoSession(t, l, 5*time.Second))
	st := madeTarget(t, r, sch, plannedTarget("t"))
	state := func() tfsdk.State {
		s := tfsdk.State{Schema: sch.Schema}
		s.Set(context.Background(), st)
		return s
	}
	// drift: the description changed by hand, a level retired by hand
	l.mu.Lock()
	l.targets["t"]["description"] = "by hand"
	l.mu.Unlock()
	resp := resource.ReadResponse{State: state()}
	r.Read(context.Background(), resource.ReadRequest{State: state()}, &resp)
	var got targetModel
	resp.State.Get(context.Background(), &got)
	if got.Description.ValueString() != "by hand" {
		t.Errorf("drift not read: %+v", got)
	}
	// gone
	l.mu.Lock()
	l.targets["t"]["metadata"].(map[string]any)["state"] = "RETIRED"
	l.mu.Unlock()
	resp = resource.ReadResponse{State: state()}
	r.Read(context.Background(), resource.ReadRequest{State: state()}, &resp)
	if !resp.State.Raw.IsNull() {
		t.Error("a retired target is removed from the state")
	}
	// delete retires
	del := resource.DeleteResponse{State: state()}
	r.Delete(context.Background(), resource.DeleteRequest{State: state()}, &del)
	if op, _ := lastOp(l); del.Diagnostics.HasError() || op != "RETIRE" {
		t.Errorf("delete: %s %s", errorText(del.Diagnostics), op)
	}
	imp := resource.ImportStateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "ledger"}, &imp)
	var im targetModel
	imp.State.Get(context.Background(), &im)
	if im.Name.ValueString() != "ledger" {
		t.Errorf("import %+v", im)
	}
}

func TestTargetNewLevelAndStateOfANewTarget(t *testing.T) {
	l := newLigoFake(t)
	r, sch := tgtRes(t, ligoSession(t, l, 5*time.Second))
	m := plannedTarget("t")
	m.State = types.StringValue("DEPRECATED")
	m.AccessLevels = types.MapValueMust(levelType, map[string]attr.Value{
		"read": levelModel{Grants: types.StringValue(`{"roles":["RO"]}`), Description: types.StringNull(), State: types.StringValue("DEPRECATED")}.obj()})
	got := madeTarget(t, r, sch, m)
	if got.State.ValueString() != "DEPRECATED" || got.levels()["read"].State.ValueString() != "DEPRECATED" || l.count() != 3 {
		t.Errorf("state %+v levels %v, %d filings", got.State, got.AccessLevels, l.count())
	}
}

func platformForms(l *ligoFake) {
	l.on("GET /v1/admin/platforms/pg", 200, `{"apiVersion":"v","kind":"Platform","name":"pg","jurisdiction":"dc/corp-prod","realm":"prod","identityProvider":{"instance":"i","type":"t"},
		"secretStore":{"instance":"s","type":"t"},"allowedSourceTrustDomains":[],"networkFlows":[],"uiSchemas":`+pgFormsJSON+`}`)
}

const pgFormsJSON = `{"targetDefinition":{"fields":[{"jsonKey":"database","fieldType":"UI_FIELD_TYPE_TEXT","required":true},{"jsonKey":"schema","fieldType":"UI_FIELD_TYPE_TEXT","required":true}]},
"accessLevelGrants":{"fields":[{"jsonKey":"roles","fieldType":"UI_FIELD_TYPE_MULTISELECT","required":true,"options":[{"value":"RO"},{"value":"RW"}]}]}}`

func planTarget(t *testing.T, r *targetResource, sch resource.SchemaResponse, m targetModel) resource.ModifyPlanResponse {
	t.Helper()
	p := tfsdk.Plan{Schema: sch.Schema}
	if d := p.Set(context.Background(), m); d.HasError() {
		t.Fatal(errorText(d))
	}
	resp := resource.ModifyPlanResponse{Plan: p}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: p, State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}, &resp)
	return resp
}

func TestTargetPlanValidatesAgainstThePlatform(t *testing.T) {
	l := newLigoFake(t)
	platformForms(l)
	s := ligoSession(t, l, time.Second)
	r, sch := tgtRes(t, s)
	if resp := planTarget(t, r, sch, plannedTarget("t")); resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 0 {
		t.Fatalf("a valid target: %v", resp.Diagnostics)
	}
	bad := plannedTarget("t")
	bad.Definition = types.StringValue(`{"database":"d","schema":"s","password":"hunter2"}`)
	bad.AccessLevels = levelsOf(map[string]string{"read": `{"roles":["ROOT"]}`})
	msg := errorText(planTarget(t, r, sch, bad).Diagnostics)
	if !strings.Contains(msg, "password is not a field") || !strings.Contains(msg, "ROOT") {
		t.Fatalf("diagnostics: %q", msg)
	}
	if l.count() != 0 {
		t.Error("a plan files nothing")
	}
	notJSON := plannedTarget("t")
	notJSON.Definition = types.StringValue(`[1]`)
	if !strings.Contains(errorText(planTarget(t, r, sch, notJSON).Diagnostics), "Not a JSON object") {
		t.Error("a definition must be an object")
	}
}

func TestTargetPlanWarnsWhenThePlatformCannotBeRead(t *testing.T) {
	l := newLigoFake(t) // no platform route: 404
	r, sch := tgtRes(t, ligoSession(t, l, time.Second))
	resp := planTarget(t, r, sch, plannedTarget("t"))
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 1 || !strings.Contains(resp.Diagnostics.Warnings()[0].Summary(), "Not validated") {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
}

func TestTargetPlanRefusesAPersonAsOwner(t *testing.T) {
	l := newLigoFake(t)
	s := ligoSession(t, l, time.Second)
	s.Person = true
	r, sch := tgtRes(t, s)
	if msg := errorText(planTarget(t, r, sch, plannedTarget("t")).Diagnostics); !strings.Contains(msg, "A person cannot own a resource") {
		t.Fatalf("diagnostics: %q", msg)
	}
}

func TestTargetSameJSONModifier(t *testing.T) {
	for name, tc := range map[string]struct {
		state, plan string
		keep        bool
	}{
		"reformatted": {`{"a":1,"b":2}`, "{\n \"b\": 2, \"a\": 1\n}", true},
		"changed":     {`{"a":1}`, `{"a":2}`, false},
		"not json":    {`{"a":1}`, `nope`, false},
	} {
		t.Run(name, func(t *testing.T) {
			resp := &planmodifier.StringResponse{PlanValue: types.StringValue(tc.plan)}
			(sameJSON{}).PlanModifyString(context.Background(), planmodifier.StringRequest{StateValue: types.StringValue(tc.state), PlanValue: types.StringValue(tc.plan)}, resp)
			if kept := resp.PlanValue.ValueString() == tc.state; kept != tc.keep {
				t.Errorf("kept %v, want %v", kept, tc.keep)
			}
		})
	}
	resp := &planmodifier.StringResponse{PlanValue: types.StringUnknown()}
	(sameJSON{}).PlanModifyString(context.Background(), planmodifier.StringRequest{StateValue: types.StringNull(), PlanValue: types.StringUnknown()}, resp)
	if !resp.PlanValue.IsUnknown() {
		t.Error("an unknown plan is left alone")
	}
	if (sameJSON{}).Description(context.Background()) == "" || (sameJSON{}).MarkdownDescription(context.Background()) == "" {
		t.Error("descriptions")
	}
}

func TestTargetConfigureAndMetadata(t *testing.T) {
	r := &targetResource{}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{}, &resp)
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: 1}, &resp)
	var m resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "ligo"}, &m)
	if !resp.Diagnostics.HasError() || m.TypeName != "ligo_target" {
		t.Errorf("%v %s", resp.Diagnostics, m.TypeName)
	}
}

// ligo wants every level in an update: the one that is retired in the same
// plan, and the one that was retired before.
func TestTargetUpdateNamesTheLevelsItRetires(t *testing.T) {
	l := newLigoFake(t)
	r, sch := tgtRes(t, ligoSession(t, l, 5*time.Second))
	m := plannedTarget("t")
	m.AccessLevels = levelsOf(map[string]string{"read": `{"roles":["RO"]}`, "write": `{"roles":["RW"]}`})
	st := madeTarget(t, r, sch, m)

	plan := st
	plan.Description = types.StringValue("changed at the same time")
	plan.AccessLevels = types.MapValueMust(levelType, map[string]attr.Value{"read": st.levels()["read"].obj()})
	got, resp := updateTarget(t, r, sch, st, plan)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if op, level := lastOp(l); op != "RETIRE_LEVEL" || level != "write" || l.count() != 3 || len(got.levels()) != 1 {
		t.Fatalf("%s %s, %d filings, levels %v", op, level, l.count(), got.AccessLevels)
	}

	// the retired level is still in ligo's list: a later update names it
	plan = got
	plan.Description = types.StringValue("again")
	if _, resp := updateTarget(t, r, sch, got, plan); resp.Diagnostics.HasError() {
		t.Fatalf("an update after a retirement: %s", errorText(resp.Diagnostics))
	}
}

func planWithState(t *testing.T, r *targetResource, sch resource.SchemaResponse, st, plan targetModel) resource.ModifyPlanResponse {
	t.Helper()
	mk := func(m targetModel) tfsdk.State {
		s := tfsdk.State{Schema: sch.Schema}
		if d := s.Set(context.Background(), m); d.HasError() {
			t.Fatal(errorText(d))
		}
		return s
	}
	p := tfsdk.Plan{Schema: sch.Schema}
	if d := p.Set(context.Background(), plan); d.HasError() {
		t.Fatal(errorText(d))
	}
	resp := resource.ModifyPlanResponse{Plan: p}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: p, State: mk(st)}, &resp)
	return resp
}

func TestTargetPlanWithAState(t *testing.T) {
	l := newLigoFake(t)
	platformForms(l)
	r, sch := tgtRes(t, ligoSession(t, l, time.Second))
	st := madeTarget(t, r, sch, plannedTarget("t"))

	// the same JSON in another form (the attribute's modifier has put the state's text back):
	// the framework marked what ligo computes as unknown, and the plan is the state, with nothing to file
	plan := st
	plan.Revision, plan.ManagedBy, plan.LastChangeRequest = types.Int64Unknown(), types.StringUnknown(), types.StringUnknown()
	resp := planWithState(t, r, sch, st, plan)
	var got targetModel
	resp.Plan.Get(context.Background(), &got)
	if resp.Diagnostics.HasError() || got.Revision.IsUnknown() || got.Definition.ValueString() != st.Definition.ValueString() {
		t.Errorf("a reformatting is no change: %s %+v", errorText(resp.Diagnostics), got)
	}

	// a real change by someone who does not own it
	other := st
	other.ManagedBy = types.StringValue("spiffe://prod.example.org/ci/other")
	changed := other
	changed.Description = types.StringValue("x")
	if msg := errorText(planWithState(t, r, sch, other, changed).Diagnostics); !strings.Contains(msg, "Managed by another automation") {
		t.Errorf("diagnostics: %q", msg)
	}

	// a level that is new gets its state from ligo
	more := st
	more.AccessLevels = types.MapValueMust(levelType, map[string]attr.Value{
		"read": st.levels()["read"].obj(),
		"new":  levelModel{Grants: types.StringValue(`{"roles":["RO"]}`), Description: types.StringNull(), State: types.StringNull()}.obj(),
	})
	more.Revision = types.Int64Unknown()
	resp = planWithState(t, r, sch, st, more)
	resp.Plan.Get(context.Background(), &got)
	if resp.Diagnostics.HasError() || !got.levels()["new"].State.IsUnknown() {
		t.Errorf("a new level's state is unknown in the plan: %s %v", errorText(resp.Diagnostics), got.AccessLevels)
	}
}

func TestTargetEffectiveManaged(t *testing.T) {
	s := &Session{DefaultManaged: true}
	m := targetModel{Managed: types.BoolNull()}
	if !m.effectiveManaged(s) {
		t.Error("the provider's default applies")
	}
	m.Managed = types.BoolValue(false)
	if m.effectiveManaged(s) {
		t.Error("the resource's own overrides")
	}
}

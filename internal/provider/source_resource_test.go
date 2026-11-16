package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const callerID = "spiffe://prod.example.org/ci/terraform"

// ligoSession is a session of an automation (an SVID, not a person) that waits
// for a request for as long as the test says.
func ligoSession(t *testing.T, l *ligoFake, wait time.Duration) *Session {
	t.Helper()
	s := l.session(t)
	s.Person, s.CallerID, s.DefaultManaged, s.ApprovalTimeout = false, callerID, true, wait
	return s
}

func srcRes(t *testing.T, s *Session) (*sourceResource, resource.SchemaResponse) {
	t.Helper()
	r := &sourceResource{session: s}
	var sch resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sch)
	return r, sch
}

// planned is the plan of a source that the configuration describes: what ligo
// computes is unknown.
func planned(name, regex string) sourceModel {
	return sourceModel{
		ID: types.StringUnknown(), Name: types.StringValue(name), TrustDomain: types.StringValue("prod.example.org"), SvidRegex: types.StringValue(regex),
		DisplayName: types.StringNull(), Description: types.StringNull(), SampleSpiffeIDs: types.ListNull(types.StringType), Labels: types.MapNull(types.StringType),
		Visibility: types.StringUnknown(), VisibleTo: types.ListNull(types.StringType), State: types.StringUnknown(), Managed: types.BoolNull(),
		Jurisdiction: types.StringUnknown(), Revision: types.Int64Unknown(), ManagedBy: types.StringUnknown(), LastChangeRequest: types.StringUnknown(),
	}
}

func create(t *testing.T, r *sourceResource, sch resource.SchemaResponse, m sourceModel) (sourceModel, resource.CreateResponse) {
	t.Helper()
	plan := tfsdk.Plan{Schema: sch.Schema}
	if d := plan.Set(context.Background(), m); d.HasError() {
		t.Fatal(errorText(d))
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: plan.Raw.Copy()}}
	resp.State.Raw = tfNull(sch)
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	var out sourceModel
	if !resp.Diagnostics.HasError() {
		resp.State.Get(context.Background(), &out)
	}
	return out, resp
}

func TestSourceCreateApplied(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	got, resp := create(t, r, sch, planned("ledger-app", "^spiffe://prod\\.example\\.org/ns/fin/sa/.*$"))
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if got.Revision.ValueInt64() != 1 || got.ManagedBy.ValueString() != callerID || got.State.ValueString() != "ACTIVE" || got.LastChangeRequest.ValueString() == "" {
		t.Errorf("state %+v", got)
	}
	if l.count() != 1 {
		t.Errorf("%d filings, want 1", l.count())
	}
	body := l.crs[l.order[0]]["payload"].(map[string]any)
	if body["managed"] != true {
		t.Errorf("the filing did not ask for the lock: %v", body)
	}
}

func TestSourceCreateUnmanaged(t *testing.T) {
	l := newLigoFake(t)
	s := ligoSession(t, l, 5*time.Second)
	s.DefaultManaged = true
	r, sch := srcRes(t, s)
	m := planned("s", "^x$")
	m.Managed = types.BoolValue(false)
	got, resp := create(t, r, sch, m)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if !got.ManagedBy.IsNull() {
		t.Errorf("managed_by %v, want null", got.ManagedBy)
	}
	if l.crs[l.order[0]]["payload"].(map[string]any)["managed"] != false {
		t.Error("managed=false must be sent")
	}
}

func TestSourceCreatePendingTimesOutWithoutState(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = false
	r, sch := srcRes(t, ligoSession(t, l, 1500*time.Millisecond))
	_, resp := create(t, r, sch, planned("s", "^x$"))
	msg := errorText(resp.Diagnostics)
	if !resp.Diagnostics.HasError() || !strings.Contains(msg, "still PENDING") || !strings.Contains(msg, "SOURCE (dc/corp-prod)") || !strings.Contains(msg, l.order[0]) {
		t.Fatalf("diagnostics: %q", msg)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("a change that did not happen must not be in the state")
	}
	if l.count() != 1 {
		t.Errorf("%d filings", l.count())
	}
}

// T-P6: the apply that follows a timeout waits for the request that is still
// open, and files nothing again.
func TestSourceRetryWaitsForTheOpenRequest(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = false
	r, sch := srcRes(t, ligoSession(t, l, 1500*time.Millisecond))
	m := planned("s", "^x$")
	if _, resp := create(t, r, sch, m); !resp.Diagnostics.HasError() {
		t.Fatal("expected a timeout")
	}
	go func() { time.Sleep(1200 * time.Millisecond); l.decide(l.order[0], "APPLIED") }()
	r.session.ApprovalTimeout = 8 * time.Second
	got, resp := create(t, r, sch, m)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if l.count() != 1 {
		t.Errorf("%d filings, want 1: the open request must be waited for", l.count())
	}
	if got.Revision.ValueInt64() != 1 {
		t.Errorf("state %+v", got)
	}
}

func TestSourceAnotherPendingContentIsAConflict(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = false
	r, sch := srcRes(t, ligoSession(t, l, 1500*time.Millisecond))
	create(t, r, sch, planned("s", "^x$"))
	_, resp := create(t, r, sch, planned("s", "^y$"))
	if !strings.Contains(errorText(resp.Diagnostics), "pending with another content") {
		t.Fatalf("diagnostics: %q", errorText(resp.Diagnostics))
	}
	if l.count() != 1 {
		t.Errorf("%d filings: nothing may be filed over another request", l.count())
	}
}

func TestSourceEndsNotApplied(t *testing.T) {
	for _, status := range []string{"REJECTED", "EXPIRED", "WITHDRAWN", "FAILED_TO_APPLY"} {
		t.Run(status, func(t *testing.T) {
			l := newLigoFake(t)
			l.immediate = false
			r, sch := srcRes(t, ligoSession(t, l, 6*time.Second))
			go func() {
				for len(l.order) == 0 {
					time.Sleep(20 * time.Millisecond)
				}
				l.decide(l.order[0], status)
			}()
			_, resp := create(t, r, sch, planned("s", "^x$"))
			if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "ended "+status) {
				t.Fatalf("diagnostics: %q", msg)
			}
			if !resp.State.Raw.IsNull() {
				t.Error("no state for a change that did not happen")
			}
		})
	}
}

func stateOf(t *testing.T, sch resource.SchemaResponse, m sourceModel) tfsdk.State {
	t.Helper()
	st := tfsdk.State{Schema: sch.Schema}
	if d := st.Set(context.Background(), m); d.HasError() {
		t.Fatal(errorText(d))
	}
	return st
}

func planOf(t *testing.T, sch resource.SchemaResponse, m sourceModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: sch.Schema}
	if d := p.Set(context.Background(), m); d.HasError() {
		t.Fatal(errorText(d))
	}
	return p
}

// created is a source that exists, in the state of its creation.
func created(t *testing.T, l *ligoFake, r *sourceResource, sch resource.SchemaResponse, m sourceModel) sourceModel {
	t.Helper()
	got, resp := create(t, r, sch, m)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	return got
}

func update(t *testing.T, r *sourceResource, sch resource.SchemaResponse, st, plan sourceModel) (sourceModel, resource.UpdateResponse) {
	t.Helper()
	resp := resource.UpdateResponse{State: stateOf(t, sch, st)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: planOf(t, sch, plan), State: stateOf(t, sch, st)}, &resp)
	var out sourceModel
	if !resp.Diagnostics.HasError() {
		resp.State.Get(context.Background(), &out)
	}
	return out, resp
}

// asPlanned turns a state into the plan of a change: the computed attributes keep their values.
func asPlanned(st sourceModel) sourceModel { return st }

func TestSourceUpdateSendsTheRevisionOfTheState(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	st := created(t, l, r, sch, planned("s", "^x$"))
	plan := asPlanned(st)
	plan.Description = types.StringValue("the ledger")
	plan.Revision = types.Int64Unknown()
	got, resp := update(t, r, sch, st, plan)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if got.Revision.ValueInt64() != 2 || got.Description.ValueString() != "the ledger" {
		t.Errorf("state %+v", got)
	}
	cr := l.crs[l.order[1]]["payload"].(map[string]any)
	if cr["expectedRevision"] != float64(1) {
		t.Errorf("expectedRevision %v, want the state's 1", cr["expectedRevision"])
	}
}

// T-P5: a stale revision is refused, said so, and never retried.
func TestSourceStaleRevisionIsNotRetried(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	st := created(t, l, r, sch, planned("s", "^x$"))
	l.mu.Lock()
	l.sources["s"]["metadata"].(map[string]any)["revision"] = int64(7) // someone changed it
	l.mu.Unlock()
	plan := asPlanned(st)
	plan.Description = types.StringValue("x")
	_, resp := update(t, r, sch, st, plan)
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "changed outside Terraform") || !strings.Contains(msg, "refresh") {
		t.Fatalf("diagnostics: %q", msg)
	}
	if l.count() != 1 {
		t.Errorf("%d filings: a stale revision must not be retried", l.count())
	}
}

func TestSourceStateChanges(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	st := created(t, l, r, sch, planned("s", "^x$"))
	plan := asPlanned(st)
	plan.State = types.StringValue("DEPRECATED")
	got, resp := update(t, r, sch, st, plan)
	if resp.Diagnostics.HasError() || got.State.ValueString() != "DEPRECATED" {
		t.Fatalf("deprecate: %v %+v", errorText(resp.Diagnostics), got)
	}
	if n := l.crs[l.order[len(l.order)-1]]["operation"]; n != "DEPRECATE" || l.count() != 2 {
		t.Errorf("operation %v, %d filings (a state change alone files no update)", n, l.count())
	}
	back := asPlanned(got)
	back.State = types.StringValue("ACTIVE")
	got, resp = update(t, r, sch, got, back)
	if resp.Diagnostics.HasError() || got.State.ValueString() != "ACTIVE" {
		t.Fatalf("reactivate: %v %+v", errorText(resp.Diagnostics), got)
	}
}

func TestSourceCreateDeprecated(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	m := planned("s", "^x$")
	m.State = types.StringValue("DEPRECATED")
	got := created(t, l, r, sch, m)
	if got.State.ValueString() != "DEPRECATED" || l.count() != 2 {
		t.Errorf("state %+v, %d filings", got, l.count())
	}
}

func TestSourceDeleteRetires(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	st := created(t, l, r, sch, planned("s", "^x$"))
	resp := resource.DeleteResponse{State: stateOf(t, sch, st)}
	r.Delete(context.Background(), resource.DeleteRequest{State: stateOf(t, sch, st)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if op := l.crs[l.order[len(l.order)-1]]["operation"]; op != "RETIRE" {
		t.Errorf("operation %v", op)
	}
}

func TestSourceReadRemovesWhatIsGone(t *testing.T) {
	for name, mutate := range map[string]func(l *ligoFake){
		"not found": func(l *ligoFake) { l.mu.Lock(); delete(l.sources, "s"); l.mu.Unlock() },
		"retired": func(l *ligoFake) {
			l.mu.Lock()
			l.sources["s"]["metadata"].(map[string]any)["state"] = "RETIRED"
			l.mu.Unlock()
		},
	} {
		t.Run(name, func(t *testing.T) {
			l := newLigoFake(t)
			r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
			st := created(t, l, r, sch, planned("s", "^x$"))
			mutate(l)
			resp := resource.ReadResponse{State: stateOf(t, sch, st)}
			r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, sch, st)}, &resp)
			if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
				t.Errorf("a resource that is gone is removed from the state: %v", errorText(resp.Diagnostics))
			}
		})
	}
}

func TestSourceReadShowsDrift(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	st := created(t, l, r, sch, planned("s", "^x$"))
	l.mu.Lock()
	l.sources["s"]["displayName"] = "Changed in the UI"
	l.sources["s"]["metadata"].(map[string]any)["revision"] = int64(9)
	l.sources["s"]["metadata"].(map[string]any)["managedBy"] = nil
	l.mu.Unlock()
	resp := resource.ReadResponse{State: stateOf(t, sch, st)}
	r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, sch, st)}, &resp)
	var got sourceModel
	resp.State.Get(context.Background(), &got)
	if got.DisplayName.ValueString() != "Changed in the UI" || got.Revision.ValueInt64() != 9 || !got.ManagedBy.IsNull() {
		t.Errorf("state %+v", got)
	}
}

func TestSourceImport(t *testing.T) {
	r, sch := srcRes(t, nil)
	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "ledger"}, &resp)
	var got sourceModel
	resp.State.Get(context.Background(), &got)
	if resp.Diagnostics.HasError() || got.Name.ValueString() != "ledger" || got.ID.ValueString() != "ledger" {
		t.Errorf("import: %v %+v", errorText(resp.Diagnostics), got)
	}
}

func modify(t *testing.T, r *sourceResource, sch resource.SchemaResponse, st *sourceModel, plan sourceModel) resource.ModifyPlanResponse {
	t.Helper()
	req := resource.ModifyPlanRequest{Plan: planOf(t, sch, plan), State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	if st != nil {
		req.State = stateOf(t, sch, *st)
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.ModifyPlan(context.Background(), req, &resp)
	return resp
}

func TestSourcePlanRefusesAPersonAsOwner(t *testing.T) {
	l := newLigoFake(t)
	s := ligoSession(t, l, time.Second)
	s.Person = true
	r, sch := srcRes(t, s)
	resp := modify(t, r, sch, nil, planned("s", "^x$"))
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "A person cannot own a resource") || !strings.Contains(msg, "managed = false") {
		t.Fatalf("diagnostics: %q", msg)
	}
	m := planned("s", "^x$")
	m.Managed = types.BoolValue(false)
	if resp := modify(t, r, sch, nil, m); resp.Diagnostics.HasError() {
		t.Errorf("a person may file what is not managed: %s", errorText(resp.Diagnostics))
	}
	if l.count() != 0 {
		t.Error("a plan files nothing")
	}
}

func TestSourcePlanRefusesAnotherOwner(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, time.Second))
	st := planned("s", "^x$")
	st.ID, st.Visibility, st.State, st.Jurisdiction, st.Revision = types.StringValue("s"), types.StringValue("PUBLIC"), types.StringValue("ACTIVE"), types.StringValue("j"), types.Int64Value(1)
	st.ManagedBy, st.LastChangeRequest = types.StringValue("spiffe://prod.example.org/ci/other"), types.StringValue("c")
	plan := st
	plan.Description = types.StringValue("changed")
	if msg := errorText(modify(t, r, sch, &st, plan).Diagnostics); !strings.Contains(msg, "Managed by another automation") {
		t.Fatalf("diagnostics: %q", msg)
	}
	if resp := modify(t, r, sch, &st, st); resp.Diagnostics.HasError() {
		t.Errorf("an unchanged resource is not an error: %s", errorText(resp.Diagnostics))
	}
}

func TestSourcePlanWarnsOfAPendingRequest(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = false
	r, sch := srcRes(t, ligoSession(t, l, time.Second))
	m := planned("s", "^x$")
	create(t, r, sch, m)
	resp := modify(t, r, sch, nil, m)
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 1 || !strings.Contains(resp.Diagnostics.Warnings()[0].Detail(), "does not file another") {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
	other := planned("s", "^y$")
	resp = modify(t, r, sch, nil, other)
	if resp.Diagnostics.WarningsCount() != 1 || !strings.Contains(resp.Diagnostics.Warnings()[0].Detail(), "another change") {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
}

func TestSourceCancelledContextFilesNothingMore(t *testing.T) {
	l := newLigoFake(t)
	l.immediate = false
	r, sch := srcRes(t, ligoSession(t, l, time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	plan := planOf(t, sch, planned("s", "^x$"))
	resp := resource.CreateResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tfNull(sch)}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "interrupted") || !strings.Contains(msg, "stays in ligo") {
		t.Fatalf("diagnostics: %q", msg)
	}
	if l.count() != 1 {
		t.Errorf("%d filings", l.count())
	}
}

func TestSourceErrorsCarryNoBody(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 2*time.Second))
	l.on("POST /v1/admin/sources", 403, `{"type":"urn:ligo:problem:forbidden","title":"forbidden","status":403,"detail":"not yours","secret":"hunter2"}`)
	_, resp := create(t, r, sch, planned("s", "^x$"))
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "may not file") || strings.Contains(msg, "hunter2") {
		t.Fatalf("diagnostics: %q", msg)
	}
}

// The state is changed after the update, with the new revision.
func TestSourceUpdateThenDeprecate(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	st := created(t, l, r, sch, planned("s", "^x$"))
	plan := asPlanned(st)
	plan.Description, plan.State, plan.Revision = types.StringValue("d"), types.StringValue("DEPRECATED"), types.Int64Unknown()
	got, resp := update(t, r, sch, st, plan)
	if resp.Diagnostics.HasError() || got.State.ValueString() != "DEPRECATED" || got.Revision.ValueInt64() != 3 || l.count() != 3 {
		t.Fatalf("%v %+v, %d filings", errorText(resp.Diagnostics), got, l.count())
	}
}

func TestSourceAllAttributesRoundTrip(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 5*time.Second))
	m := planned("s", "^spiffe://x/a$")
	m.DisplayName, m.Description = types.StringValue("Ledger"), types.StringValue("the ledger")
	m.Labels = types.MapValueMust(types.StringType, map[string]attr.Value{"team": types.StringValue("fin")})
	m.SampleSpiffeIDs = strsV([]string{"spiffe://x/a"}).(types.List)
	m.Visibility, m.VisibleTo = types.StringValue("SELECTED"), strsV([]string{"dc/*"}).(types.List)
	st := created(t, l, r, sch, m)
	resp := resource.ReadResponse{State: stateOf(t, sch, st)}
	r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, sch, st)}, &resp)
	var got sourceModel
	resp.State.Get(context.Background(), &got)
	if resp.Diagnostics.HasError() || got.Labels.Elements()["team"].(types.String).ValueString() != "fin" || got.Visibility.ValueString() != "SELECTED" ||
		len(got.VisibleTo.Elements()) != 1 || len(got.SampleSpiffeIDs.Elements()) != 1 || got.DisplayName.ValueString() != "Ledger" {
		t.Errorf("read %v %+v", errorText(resp.Diagnostics), got)
	}
	if res := stateOf(t, sch, got); res.Raw.IsNull() {
		t.Error("no state")
	}
}

func TestSourceRemovalRefusedWhileBindingsExist(t *testing.T) {
	l := newLigoFake(t)
	r, sch := srcRes(t, ligoSession(t, l, 2*time.Second))
	st := created(t, l, r, sch, planned("s", "^x$"))
	l.on("POST /v1/admin/sources/s/retire", 409, `{"type":"urn:ligo:problem:resource-in-use","title":"in use","status":409,"detail":"2 bindings use it"}`)
	resp := resource.DeleteResponse{State: stateOf(t, sch, st)}
	r.Delete(context.Background(), resource.DeleteRequest{State: stateOf(t, sch, st)}, &resp)
	if msg := errorText(resp.Diagnostics); !strings.Contains(msg, "bindings") || !strings.Contains(msg, "refuses the change") {
		t.Fatalf("diagnostics: %q", msg)
	}
}

func TestSourceConfigure(t *testing.T) {
	r := &sourceResource{}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{}, &resp)
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "wrong"}, &resp)
	if !resp.Diagnostics.HasError() || r.session != nil {
		t.Error("a wrong provider data is an error")
	}
	var m resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "ligo"}, &m)
	if m.TypeName != "ligo_source" {
		t.Errorf("type %s", m.TypeName)
	}
}

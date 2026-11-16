package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/dihedron/ligo/pkg/adminclient"
	"github.com/dihedron/terraform-provider-ligo/internal/jsonnorm"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// targetResource is ligo_target (spec §3.2).
type targetResource struct{ session *Session }

var (
	_ resource.Resource                = (*targetResource)(nil)
	_ resource.ResourceWithConfigure   = (*targetResource)(nil)
	_ resource.ResourceWithImportState = (*targetResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*targetResource)(nil)
)

func newTargetResource() resource.Resource { return &targetResource{} }

type targetModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Platform          types.String `tfsdk:"platform"`
	DisplayName       types.String `tfsdk:"display_name"`
	Description       types.String `tfsdk:"description"`
	DocumentationURL  types.String `tfsdk:"documentation_url"`
	Definition        types.String `tfsdk:"definition"`
	AccessLevels      types.Map    `tfsdk:"access_levels"`
	NetworkFlows      types.String `tfsdk:"network_flows"`
	Labels            types.Map    `tfsdk:"labels"`
	Visibility        types.String `tfsdk:"visibility"`
	VisibleTo         types.List   `tfsdk:"visible_to"`
	State             types.String `tfsdk:"state"`
	Managed           types.Bool   `tfsdk:"managed"`
	Jurisdiction      types.String `tfsdk:"jurisdiction"`
	Revision          types.Int64  `tfsdk:"revision"`
	ManagedBy         types.String `tfsdk:"managed_by"`
	LastChangeRequest types.String `tfsdk:"last_change_request"`
}

// levelModel is one access level, keyed by its name in access_levels.
type levelModel struct {
	Grants      types.String `tfsdk:"grants"`
	Description types.String `tfsdk:"description"`
	State       types.String `tfsdk:"state"`
}

var levelType = types.ObjectType{AttrTypes: map[string]attr.Type{"grants": types.StringType, "description": types.StringType, "state": types.StringType}}

// sameJSON keeps the state's text when the plan says the same JSON in another
// form, so that a reformatting is no diff (spec §3.5).
type sameJSON struct{}

func (sameJSON) Description(context.Context) string {
	return "A JSON text that means what the state holds is no change."
}
func (m sameJSON) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }
func (sameJSON) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	a, err1 := jsonnorm.Normalise(req.StateValue.ValueString())
	b, err2 := jsonnorm.Normalise(req.PlanValue.ValueString())
	if err1 == nil && err2 == nil && a == b {
		resp.PlanValue = req.StateValue
	}
}

func (r *targetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_target"
}

func (r *targetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	same := []planmodifier.String{sameJSON{}}
	resp.Schema = schema.Schema{
		Description: "A target: a resource that workloads reach through bindings, on a platform. Every change is a change request that the provider files once and waits for; " +
			"it never approves one. The definition and the grants name credentials by reference, never by value.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "The name."},
			"name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Description: "The name; a new name is a new resource."},
			"platform": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Description: "The platform that serves it; a target does not move to another."},
			"display_name":      schema.StringAttribute{Optional: true, Description: "A name for people."},
			"description":       schema.StringAttribute{Optional: true, Description: "What it is."},
			"documentation_url": schema.StringAttribute{Optional: true, Description: "Where its documentation is."},
			"definition": schema.StringAttribute{Required: true, PlanModifiers: same,
				Description: "What the platform's plugin needs to reach the target, as JSON text (jsonencode). It is checked against the platform's form when the plan can read it."},
			"access_levels": schema.MapNestedAttribute{Required: true, Description: "The access levels, by name.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"grants":      schema.StringAttribute{Required: true, PlanModifiers: same, Description: "What the level grants, as JSON text. A change may only narrow it: ligo refuses a widening."},
					"description": schema.StringAttribute{Optional: true, Description: "What the level is for."},
					"state": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keep,
						Description: "ACTIVE (the default) or DEPRECATED. A level that leaves the map is retired."},
				}}},
			"network_flows": schema.StringAttribute{Optional: true, PlanModifiers: same, Description: "The network flows the target needs, as JSON text."},
			"labels":        schema.MapAttribute{Optional: true, ElementType: types.StringType, Description: "Labels."},
			"visibility":    schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keep, Description: "Who may see it in the catalog: PUBLIC, PRIVATE or SELECTED. Default: ligo's."},
			"visible_to":    schema.ListAttribute{Optional: true, ElementType: types.StringType, Description: "With SELECTED, the jurisdiction patterns that may see it."},
			"state":         schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keep, Description: "ACTIVE (the default) or DEPRECATED (no new bindings). Removal retires it."},
			"managed": schema.BoolAttribute{Optional: true, Description: "Whether this automation owns it (the lock): only the owner then changes it. Default: the provider's managed. " +
				"false, on one that is managed, is the owner's release."},
			"jurisdiction": schema.StringAttribute{Required: true, Description: "The jurisdiction of the target, among those the platform allows (its target_jurisdictions)."},
			"revision":     schema.Int64Attribute{Computed: true, Description: "The revision; an update names it, and ligo refuses a stale one."},
			"managed_by":   schema.StringAttribute{Computed: true, Description: "The SPIFFE ID of the automation that owns it; null when nobody does."},
			"last_change_request": schema.StringAttribute{Computed: true,
				Description: "The ID of the last change request the provider filed or waited for."},
		},
	}
}

func (r *targetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	s, ok := req.ProviderData.(*Session)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("want *Session, got %T", req.ProviderData))
		return
	}
	r.session = s
}

func (m targetModel) effectiveManaged(s *Session) bool {
	if m.Managed.IsNull() || m.Managed.IsUnknown() {
		return s.DefaultManaged
	}
	return m.Managed.ValueBool()
}

// levels is the access levels of the model, by name.
func (m targetModel) levels() map[string]levelModel {
	out := map[string]levelModel{}
	for k, v := range m.AccessLevels.Elements() {
		var l levelModel
		if obj, ok := v.(types.Object); ok {
			_ = obj.As(context.Background(), &l, objectAsOptions)
		}
		out[k] = l
	}
	return out
}

func jsonOrNil(s types.String) any {
	if s.IsNull() || s.IsUnknown() || s.ValueString() == "" {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(s.ValueString()), &v) != nil {
		return nil
	}
	return v
}

// body is the spec as a request carries it, for the levels in keep (all when
// nil): it is also what a pending request of ours is recognised by.
func (m targetModel) body(keep func(string) bool, extra ...any) map[string]any {
	meta := map[string]any{"name": m.Name.ValueString(), "platform": m.Platform.ValueString()}
	if v := optStr(m.Jurisdiction); v != nil {
		meta["jurisdiction"] = *v
	}
	levels := []any{}
	ls := m.levels()
	names := make([]string, 0, len(ls))
	for n := range ls {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if keep != nil && !keep(n) {
			continue
		}
		l := map[string]any{"name": n, "grants": jsonOrNil(ls[n].Grants)}
		if v := optStr(ls[n].Description); v != nil {
			l["description"] = *v
		}
		levels = append(levels, l)
	}
	levels = append(levels, extra...)
	b := map[string]any{"kind": "Target", "metadata": meta, "definition": jsonOrNil(m.Definition), "accessLevels": levels}
	for k, v := range map[string]*string{"displayName": optStr(m.DisplayName), "description": optStr(m.Description), "documentationUrl": optStr(m.DocumentationURL)} {
		if v != nil {
			b[k] = *v
		}
	}
	if v := jsonOrNil(m.NetworkFlows); v != nil {
		b["networkFlows"] = v
	}
	if l := strMap(m.Labels); l != nil {
		b["labels"] = *l
	}
	if v := optStr(m.Visibility); v != nil {
		vis := map[string]any{"mode": *v}
		if l := strList(m.VisibleTo); l != nil {
			vis["visibleTo"] = *l
		}
		b["visibility"] = vis
	}
	return b
}

func (m targetModel) spec(extra ...any) (adminclient.TargetSpec, error) {
	var spec adminclient.TargetSpec
	raw, err := json.Marshal(m.body(nil, extra...))
	if err == nil {
		err = json.Unmarshal(raw, &spec)
	}
	return spec, err
}

// fill sets what comes from ligo, keeping the text of what the user wrote when
// it means the same (the plan modifier does the same for a plan).
func (m *targetModel) fill(t adminclient.Target) error {
	m.ID, m.Name = types.StringValue(t.Metadata.Name), types.StringValue(t.Metadata.Name)
	m.Platform, m.Jurisdiction = types.StringValue(t.Metadata.Platform), types.StringValue(t.Metadata.Jurisdiction)
	m.State, m.Revision = types.StringValue(string(t.Metadata.State)), types.Int64Value(t.Metadata.Revision)
	m.Visibility, m.ManagedBy = types.StringValue(string(t.Visibility.Mode)), types.StringPointerValue(t.Metadata.ManagedBy)
	def, err := jsonnorm.Of(t.Definition)
	if err != nil {
		return fmt.Errorf("definition: %w", err)
	}
	m.Definition = keepIfSame(m.Definition, def)
	prior := m.levels()
	levels := map[string]attr.Value{}
	for _, lv := range t.AccessLevels {
		if lv.State != nil && *lv.State == "RETIRED" {
			continue // a retired level has left the target
		}
		g, err := jsonnorm.Of(lv.Grants)
		if err != nil {
			return fmt.Errorf("grants of %s: %w", lv.Name, err)
		}
		state := types.StringNull()
		if lv.State != nil {
			state = types.StringValue(string(*lv.State))
		}
		levels[lv.Name] = types.ObjectValueMust(levelType.AttrTypes, map[string]attr.Value{
			"grants": keepIfSame(prior[lv.Name].Grants, g), "description": types.StringPointerValue(lv.Description), "state": state})
	}
	m.AccessLevels = types.MapValueMust(levelType, levels)
	if t.NetworkFlows != nil && len(*t.NetworkFlows) > 0 {
		nf, err := jsonnorm.Of(*t.NetworkFlows)
		if err != nil {
			return fmt.Errorf("network_flows: %w", err)
		}
		m.NetworkFlows = keepIfSame(m.NetworkFlows, nf)
	}
	return nil
}

// keepIfSame is the user's text when it is the same JSON as ligo's, and
// ligo's normal form otherwise.
func keepIfSame(prior types.String, normal string) types.String {
	if !prior.IsNull() && !prior.IsUnknown() {
		if p, err := jsonnorm.Normalise(prior.ValueString()); err == nil && p == normal {
			return prior
		}
	}
	return types.StringValue(normal)
}

func (r *targetResource) readBack(ctx context.Context, m *targetModel, cr string, diags *diag.Diagnostics) bool {
	t, err := r.session.Client.GetTarget(ctx, m.Name.ValueString())
	if err != nil {
		diags.AddError("Cannot read the target after the change", err.Error())
		return false
	}
	if err := m.fill(adminclient.Target{AccessLevels: t.AccessLevels, Definition: t.Definition, Metadata: t.Metadata, Visibility: t.Visibility, NetworkFlows: t.NetworkFlows}); err != nil {
		diags.AddError("Cannot read the target after the change", err.Error())
		return false
	}
	m.LastChangeRequest = types.StringValue(cr)
	return true
}

func (r *targetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan targetModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	managed := plan.effectiveManaged(r.session)
	spec, err := plan.spec()
	if err != nil {
		resp.Diagnostics.AddError("Cannot build the request", err.Error())
		return
	}
	want, levelWant := optStr(plan.State), plan.levels()
	cr, err := r.session.apply(ctx, filing{kind: "TARGET", name: plan.Name.ValueString(), payload: plan.body(nil),
		file: func(ctx context.Context) (adminclient.ChangeRequest, error) {
			return r.session.Client.CreateTarget(ctx, spec, r.session.options(&managed))
		}})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the target", err.Error())
		return
	}
	if !r.readBack(ctx, &plan, cr.Id.String(), &resp.Diagnostics) {
		return
	}
	// what was asked beyond the creation: levels and the target in another state
	r.settle(ctx, &plan, want, levelWant, nil, viewOf(plan), &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...) // the target exists, even if a later step failed
}

// view is what ligo holds of a target's states: the target's, and its levels'.
type view struct {
	state  string
	levels map[string]string
}

func viewOf(m targetModel) view {
	v := view{state: m.State.ValueString(), levels: map[string]string{}}
	for n, l := range m.levels() {
		v.levels[n] = l.State.ValueString()
	}
	return v
}

// settle brings the access levels' states and the target's state to what was
// asked (want and levels), from what ligo holds (cur), one change request
// each, and retires the levels in retire. After each step m is what ligo holds.
func (r *targetResource) settle(ctx context.Context, m *targetModel, want *string, levels map[string]levelModel, retire []string, cur view, diags *diag.Diagnostics) {
	name := m.Name.ValueString()
	step := func(what string, call func(context.Context, int64) (adminclient.ChangeRequest, error)) bool {
		cr, err := r.session.stateChange(ctx, "TARGET", name, func(ctx context.Context) (adminclient.ChangeRequest, error) {
			return call(ctx, m.Revision.ValueInt64())
		})
		if err != nil {
			diags.AddError("Cannot "+what, err.Error())
			return false
		}
		return r.readBack(ctx, m, cr.Id.String(), diags)
	}
	c := r.session.Client
	for _, lv := range retire {
		if !step("retire the access level "+lv, func(ctx context.Context, rev int64) (adminclient.ChangeRequest, error) {
			return c.RetireAccessLevel(ctx, name, lv, rev, r.session.options(nil))
		}) {
			return
		}
	}
	names := make([]string, 0, len(levels))
	for n := range levels {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		to := optStr(levels[n].State)
		if to == nil || *to == cur.levels[n] || cur.levels[n] == "" {
			continue
		}
		call := c.ReactivateAccessLevel
		if *to == "DEPRECATED" {
			call = c.DeprecateAccessLevel
		}
		if !step("change the state of the access level "+n, func(ctx context.Context, rev int64) (adminclient.ChangeRequest, error) {
			return call(ctx, name, n, rev, r.session.options(nil))
		}) {
			return
		}
	}
	if want != nil && *want != cur.state {
		call := c.ReactivateTarget
		if *want == "DEPRECATED" {
			call = c.DeprecateTarget
		}
		step("change the state of the target", func(ctx context.Context, rev int64) (adminclient.ChangeRequest, error) {
			return call(ctx, name, rev, r.session.options(nil))
		})
	}
}

func (r *targetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st targetModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
		return
	}
	t, err := r.session.Client.GetTarget(ctx, st.Name.ValueString())
	if errors.Is(err, adminclient.ErrNotFound) || (err == nil && t.Metadata.State == "RETIRED") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		readError(&resp.Diagnostics, "ligo_target "+st.Name.ValueString(), err)
		return
	}
	if err := st.fill(adminclient.Target{AccessLevels: t.AccessLevels, Definition: t.Definition, Metadata: t.Metadata, Visibility: t.Visibility, NetworkFlows: t.NetworkFlows}); err != nil {
		resp.Diagnostics.AddError("Cannot read the target", err.Error())
		return
	}
	st.DisplayName, st.Description = types.StringPointerValue(t.DisplayName), types.StringPointerValue(t.Description)
	st.DocumentationURL = types.StringPointerValue(t.DocumentationUrl)
	st.Labels = labelsOrNull(t.Labels, st.Labels)
	if t.Visibility.VisibleTo != nil {
		st.VisibleTo = listOf(*t.Visibility.VisibleTo)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, st)...)
}

func (r *targetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, st targetModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
		return
	}
	plan.Revision, plan.ManagedBy, plan.LastChangeRequest = st.Revision, st.ManagedBy, st.LastChangeRequest
	managed, want := plan.effectiveManaged(r.session), optStr(plan.State)
	levelWant, have := plan.levels(), st.levels()
	cur := viewOf(st)
	var retire []string
	for n := range have {
		if _, ok := levelWant[n]; !ok {
			retire = append(retire, n)
		}
	}
	sort.Strings(retire)
	if r.changed(plan, st, retire) {
		// ligo wants every level in an update: the ones to retire next, and the ones it already retired
		extra, err := r.keptLevels(ctx, plan.Name.ValueString(), st, retire)
		if err != nil {
			resp.Diagnostics.AddError("Cannot update the target", err.Error())
			return
		}
		spec, err := plan.spec(extra...)
		if err != nil {
			resp.Diagnostics.AddError("Cannot build the request", err.Error())
			return
		}
		cr, err := r.session.apply(ctx, filing{kind: "TARGET", name: plan.Name.ValueString(), payload: plan.body(nil, extra...),
			file: func(ctx context.Context) (adminclient.ChangeRequest, error) {
				return r.session.Client.UpdateTarget(ctx, plan.Name.ValueString(), spec, st.Revision.ValueInt64(), r.session.options(&managed))
			}})
		if err != nil {
			resp.Diagnostics.AddError("Cannot update the target", err.Error())
			return
		}
		if !r.readBack(ctx, &plan, cr.Id.String(), &resp.Diagnostics) {
			return
		}
		cur = viewOf(plan)
	}
	r.settle(ctx, &plan, want, levelWant, retire, cur, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// keptLevels are the levels that an update must still name though the plan
// does not: those it is about to retire (as the state has them), and those that
// ligo retired before (as it shows them).
func (r *targetResource) keptLevels(ctx context.Context, name string, st targetModel, retire []string) ([]any, error) {
	var out []any
	have := st.levels()
	for _, n := range retire {
		l := map[string]any{"name": n, "grants": jsonOrNil(have[n].Grants)}
		if v := optStr(have[n].Description); v != nil {
			l["description"] = *v
		}
		out = append(out, l)
	}
	t, err := r.session.Client.GetTarget(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("reading the access levels of %s: %w", name, err)
	}
	for _, lv := range t.AccessLevels {
		if lv.State != nil && *lv.State == "RETIRED" {
			l := map[string]any{"name": lv.Name, "grants": lv.Grants, "state": "RETIRED"}
			if lv.Description != nil {
				l["description"] = *lv.Description
			}
			out = append(out, l)
		}
	}
	return out, nil
}

// changed tells whether the plan changes what UpdateTarget files (the lock
// included): the bodies differ once the levels that are retired are left out.
func (r *targetResource) changed(plan, st targetModel, retire []string) bool {
	if !plan.Managed.Equal(st.Managed) {
		return true
	}
	keep := func(n string) bool { return !slices.Contains(retire, n) }
	a, b := plan, st
	for _, p := range []*targetModel{&a, &b} {
		p.State = types.StringNull()
		if p.Visibility.IsUnknown() {
			p.Visibility = types.StringNull()
		}
	}
	if plan.Visibility.IsUnknown() {
		b.Visibility = types.StringNull()
	}
	// the levels' states are changed apart: leave them out of the comparison
	a.AccessLevels, b.AccessLevels = stripStates(a.AccessLevels), stripStates(b.AccessLevels)
	x, _ := jsonnorm.Of(a.body(keep))
	y, _ := jsonnorm.Of(b.body(keep))
	return x != y
}

func (lm levelModel) obj() attr.Value {
	return types.ObjectValueMust(levelType.AttrTypes, map[string]attr.Value{"grants": lm.Grants, "description": lm.Description, "state": lm.State})
}

func stripStates(m types.Map) types.Map {
	out := map[string]attr.Value{}
	for k, v := range m.Elements() {
		o := v.(types.Object)
		attrs := o.Attributes()
		out[k] = types.ObjectValueMust(levelType.AttrTypes, map[string]attr.Value{"grants": attrs["grants"], "description": attrs["description"], "state": types.StringNull()})
	}
	return types.MapValueMust(levelType, out)
}

func (r *targetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st targetModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
		return
	}
	name, rev := st.Name.ValueString(), st.Revision.ValueInt64()
	if _, err := r.session.stateChange(ctx, "TARGET", name, func(ctx context.Context) (adminclient.ChangeRequest, error) {
		return r.session.Client.RetireTarget(ctx, name, rev, r.session.options(nil))
	}); err != nil {
		resp.Diagnostics.AddError("Cannot retire the target", err.Error())
	}
}

func (r *targetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ModifyPlan refuses what ligo would refuse, and what the platform's form
// refuses, before any filing.
func (r *targetResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.session == nil {
		return
	}
	var plan, st targetModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	if !req.State.Raw.IsNull() {
		if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
			return
		}
	}
	// A level that is new has no state in the plan: ligo sets it.
	levels, fixed := map[string]attr.Value{}, false
	for n, lv := range plan.levels() {
		if lv.State.IsNull() {
			lv.State, fixed = types.StringUnknown(), true
		}
		levels[n] = lv.obj()
	}
	if fixed {
		plan.AccessLevels = types.MapValueMust(levelType, levels)
		if resp.Diagnostics.Append(resp.Plan.Set(ctx, plan)...); resp.Diagnostics.HasError() {
			return
		}
	}
	if !req.State.Raw.IsNull() {
		// A JSON text that was only reformatted leaves the plan different from the
		// state, which makes the framework mark what ligo computes as unknown. When
		// nothing else differs the plan is the state: there is nothing to file.
		same := plan
		same.Revision, same.ManagedBy, same.LastChangeRequest = st.Revision, st.ManagedBy, st.LastChangeRequest
		np := tfsdk.Plan{Schema: req.Plan.Schema}
		if !np.Set(ctx, same).HasError() && np.Raw.Equal(req.State.Raw) {
			resp.Plan = np
			return
		}
	}
	changes := req.State.Raw.IsNull() || !req.State.Raw.Equal(resp.Plan.Raw)
	r.session.lockChecks(ctx, "TARGET", plan.Name.ValueString(), plan.effectiveManaged(r.session), st.ManagedBy, !req.State.Raw.IsNull(), changes, plan.body(nil), &resp.Diagnostics)
	if changes && !resp.Diagnostics.HasError() {
		r.validate(ctx, plan, &resp.Diagnostics)
	}
}

// validate checks the definition and the grants against the platform's forms.
// A platform that cannot be read is a warning, not an error: ligo checks anyway.
func (r *targetResource) validate(ctx context.Context, plan targetModel, diags *diag.Diagnostics) {
	if plan.Platform.IsUnknown() || plan.Definition.IsUnknown() {
		return
	}
	p, err := r.session.Client.GetPlatform(ctx, plan.Platform.ValueString())
	if err != nil || p.UiSchemas == nil {
		diags.AddWarning("Not validated at plan time", fmt.Sprintf("The forms of the platform %s could not be read, so the definition and the grants are checked by ligo at the apply.", plan.Platform.ValueString()))
		return
	}
	forms := *p.UiSchemas
	check := func(at path.Path, form string, v types.String) {
		if v.IsUnknown() || v.IsNull() {
			return
		}
		f, _ := forms[form].(map[string]any)
		vals, ok := jsonOrNil(v).(map[string]any)
		if !ok {
			diags.AddAttributeError(at, "Not a JSON object", "It must be a JSON object, as jsonencode({...}) makes.")
			return
		}
		for _, problem := range validateForm(f, vals) {
			diags.AddAttributeError(at, "Not valid for the platform "+plan.Platform.ValueString(), problem)
		}
	}
	check(path.Root("definition"), "targetDefinition", plan.Definition)
	for n, lv := range plan.levels() {
		check(path.Root("access_levels").AtMapKey(n).AtName("grants"), "accessLevelGrants", lv.Grants)
	}
}

var objectAsOptions = basetypes.ObjectAsOptions{}

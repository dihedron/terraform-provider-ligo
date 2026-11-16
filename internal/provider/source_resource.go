package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/dihedron/ligo/pkg/adminclient"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// sourceResource is ligo_source (spec §3.2).
type sourceResource struct{ session *Session }

var (
	_ resource.Resource                = (*sourceResource)(nil)
	_ resource.ResourceWithConfigure   = (*sourceResource)(nil)
	_ resource.ResourceWithImportState = (*sourceResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*sourceResource)(nil)
)

func newSourceResource() resource.Resource { return &sourceResource{} }

type sourceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	DisplayName       types.String `tfsdk:"display_name"`
	Description       types.String `tfsdk:"description"`
	TrustDomain       types.String `tfsdk:"trust_domain"`
	SvidRegex         types.String `tfsdk:"svid_regex"`
	SampleSpiffeIDs   types.List   `tfsdk:"sample_spiffe_ids"`
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

func (r *sourceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_source"
}

func (r *sourceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A source: a set of workloads, named by a SPIFFE ID regex. Every change is a change request that the provider files once and waits for; " +
			"it never approves one. A person who may not decide waits for an approver (approval_timeout in the provider).",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "The name."},
			"name":              schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Description: "The name; a new name is a new resource."},
			"display_name":      schema.StringAttribute{Optional: true, Description: "A name for people."},
			"description":       schema.StringAttribute{Optional: true, Description: "What it is."},
			"trust_domain":      schema.StringAttribute{Required: true, Description: "The trust domain of the SPIFFE IDs it matches."},
			"svid_regex":        schema.StringAttribute{Required: true, Description: "The SPIFFE ID regex; an update may only narrow it, and ligo refuses a widening."},
			"sample_spiffe_ids": schema.ListAttribute{Optional: true, ElementType: types.StringType, Description: "SPIFFE IDs the regex must match, checked by ligo."},
			"labels":            schema.MapAttribute{Optional: true, ElementType: types.StringType, Description: "Labels."},
			"visibility":        schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keep, Description: "Who may see it in the catalog: PUBLIC, PRIVATE or SELECTED. Default: ligo's."},
			"visible_to":        schema.ListAttribute{Optional: true, ElementType: types.StringType, Description: "With SELECTED, the jurisdiction patterns that may see it."},
			"state":             schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keep, Description: "ACTIVE (the default) or DEPRECATED (no new bindings). Removal retires it."},
			"managed": schema.BoolAttribute{Optional: true, Description: "Whether this automation owns it (the lock): only the owner then changes it. Default: the provider's managed. " +
				"false, on one that is managed, is the owner's release."},
			"jurisdiction":        schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "The jurisdiction, derived from the regex."},
			"revision":            schema.Int64Attribute{Computed: true, Description: "The revision; an update names it, and ligo refuses a stale one."},
			"managed_by":          schema.StringAttribute{Computed: true, Description: "The SPIFFE ID of the automation that owns it; null when nobody does."},
			"last_change_request": schema.StringAttribute{Computed: true, Description: "The ID of the last change request the provider filed or waited for."},
		},
	}
}

func (r *sourceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// effectiveManaged is the lock this resource asks for.
func (m sourceModel) effectiveManaged(s *Session) bool {
	if m.Managed.IsNull() || m.Managed.IsUnknown() {
		return s.DefaultManaged
	}
	return m.Managed.ValueBool()
}

func strList(l types.List) *[]string {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	var out []string
	for _, e := range l.Elements() {
		out = append(out, e.(types.String).ValueString())
	}
	return &out
}

func strMap(m types.Map) *map[string]string {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	out := map[string]string{}
	for k, e := range m.Elements() {
		out[k] = e.(types.String).ValueString()
	}
	return &out
}

func optStr(s types.String) *string {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	v := s.ValueString()
	return &v
}

// body is the spec as a request carries it: the map is what a pending request
// of ours is recognised by, and what the typed spec is decoded from (the
// client's nested types are not exported).
func (m sourceModel) body() map[string]any {
	identity := map[string]any{"trustDomain": m.TrustDomain.ValueString(), "svidRegex": m.SvidRegex.ValueString()}
	if l := strList(m.SampleSpiffeIDs); l != nil {
		identity["sampleSpiffeIds"] = *l
	}
	b := map[string]any{"kind": "Source", "metadata": map[string]any{"name": m.Name.ValueString()}, "identity": identity}
	if v := optStr(m.DisplayName); v != nil {
		b["displayName"] = *v
	}
	if v := optStr(m.Description); v != nil {
		b["description"] = *v
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

func (m sourceModel) spec() (adminclient.SourceSpec, error) {
	var spec adminclient.SourceSpec
	raw, err := json.Marshal(m.body())
	if err == nil {
		err = json.Unmarshal(raw, &spec)
	}
	return spec, err
}

// fill sets the attributes that come from ligo.
func (m *sourceModel) fill(s adminclient.Source) {
	m.ID, m.Name = types.StringValue(s.Metadata.Name), types.StringValue(s.Metadata.Name)
	m.TrustDomain, m.SvidRegex = types.StringValue(s.Identity.TrustDomain), types.StringValue(s.Identity.SvidRegex)
	m.Jurisdiction, m.State = types.StringValue(s.Metadata.Jurisdiction), types.StringValue(string(s.Metadata.State))
	m.Revision = types.Int64Value(s.Metadata.Revision)
	m.Visibility = types.StringValue(string(s.Visibility.Mode))
	m.ManagedBy = types.StringPointerValue(s.Metadata.ManagedBy)
}

// readBack reads the source the change made, to fill the computed attributes.
func (r *sourceResource) readBack(ctx context.Context, m *sourceModel, cr string, diags *diag.Diagnostics) bool {
	s, err := r.session.Client.GetSource(ctx, m.Name.ValueString())
	if err != nil {
		diags.AddError("Cannot read the source after the change", err.Error())
		return false
	}
	src := adminclient.Source{Identity: s.Identity, Metadata: s.Metadata, Visibility: s.Visibility}
	m.fill(src)
	m.LastChangeRequest = types.StringValue(cr)
	return true
}

func (r *sourceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	managed := plan.effectiveManaged(r.session)
	spec, err := plan.spec()
	if err != nil {
		resp.Diagnostics.AddError("Cannot build the request", err.Error())
		return
	}
	cr, err := r.session.apply(ctx, filing{kind: "SOURCE", name: plan.Name.ValueString(), payload: plan.body(),
		file: func(ctx context.Context) (adminclient.ChangeRequest, error) {
			return r.session.Client.CreateSource(ctx, spec, r.session.options(&managed))
		}})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the source", err.Error())
		return
	}
	want := optStr(plan.State) // the read-back overwrites it with what ligo has
	if !r.readBack(ctx, &plan, cr.Id.String(), &resp.Diagnostics) {
		return
	}
	if want != nil && *want == "DEPRECATED" {
		if r.setState(ctx, &plan, "DEPRECATED", &resp.Diagnostics); resp.Diagnostics.HasError() {
			// the source exists: keep it in the state, so that the next apply finishes the job
			resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// setState deprecates or reactivates the source, as one more change request.
func (r *sourceResource) setState(ctx context.Context, m *sourceModel, to string, diags *diag.Diagnostics) {
	name, rev := m.Name.ValueString(), m.Revision.ValueInt64()
	call := r.session.Client.ReactivateSource
	if to == "DEPRECATED" {
		call = r.session.Client.DeprecateSource
	}
	cr, err := r.session.apply(ctx, filing{kind: "SOURCE", name: name, payload: nil,
		file: func(ctx context.Context) (adminclient.ChangeRequest, error) {
			return call(ctx, name, rev, r.session.options(nil)) // a state change carries no lock
		}})
	if err != nil {
		diags.AddError("Cannot change the state of the source", err.Error())
		return
	}
	r.readBack(ctx, m, cr.Id.String(), diags)
}

func (r *sourceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st sourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
		return
	}
	s, err := r.session.Client.GetSource(ctx, st.Name.ValueString())
	if errors.Is(err, adminclient.ErrNotFound) || (err == nil && s.Metadata.State == "RETIRED") {
		resp.State.RemoveResource(ctx) // gone, or retired by somebody else
		return
	}
	if err != nil {
		readError(&resp.Diagnostics, "ligo_source "+st.Name.ValueString(), err)
		return
	}
	st.fill(adminclient.Source{Identity: s.Identity, Metadata: s.Metadata, Visibility: s.Visibility})
	st.DisplayName, st.Description = types.StringPointerValue(s.DisplayName), types.StringPointerValue(s.Description)
	st.Labels = labelsOrNull(s.Labels, st.Labels)
	st.SampleSpiffeIDs = sampleIDs(s.Identity.SampleSpiffeIds, st.SampleSpiffeIDs)
	if s.Visibility.VisibleTo != nil {
		st.VisibleTo = listOf(*s.Visibility.VisibleTo)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, st)...)
}

// labelsOrNull is ligo's labels; an empty map from ligo is the null the user left.
func labelsOrNull(l *map[string]string, prior types.Map) types.Map {
	if l == nil || len(*l) == 0 {
		if prior.IsNull() {
			return prior
		}
		return types.MapValueMust(types.StringType, map[string]attr.Value{})
	}
	return labelsV(l).(types.Map)
}

func sampleIDs(ids *[]string, prior types.List) types.List {
	if ids == nil || len(*ids) == 0 {
		return prior // ligo keeps what the user gave; none is none
	}
	return listOf(*ids)
}

func listOf(l []string) types.List { return strsV(l).(types.List) }

func (r *sourceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, st sourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
		return
	}
	plan.Revision, plan.ManagedBy, plan.Jurisdiction, plan.LastChangeRequest = st.Revision, st.ManagedBy, st.Jurisdiction, st.LastChangeRequest
	managed := plan.effectiveManaged(r.session)
	want, current := optStr(plan.State), st.State.ValueString() // the read-back overwrites the plan with what ligo has
	if r.specChanged(plan, st) {
		spec, err := plan.spec()
		if err != nil {
			resp.Diagnostics.AddError("Cannot build the request", err.Error())
			return
		}
		cr, err := r.session.apply(ctx, filing{kind: "SOURCE", name: plan.Name.ValueString(), payload: plan.body(),
			file: func(ctx context.Context) (adminclient.ChangeRequest, error) {
				return r.session.Client.UpdateSource(ctx, plan.Name.ValueString(), spec, st.Revision.ValueInt64(), r.session.options(&managed))
			}})
		if err != nil {
			resp.Diagnostics.AddError("Cannot update the source", err.Error())
			return
		}
		if !r.readBack(ctx, &plan, cr.Id.String(), &resp.Diagnostics) {
			return
		}
		current = plan.State.ValueString()
	}
	if want != nil && *want != current {
		r.setState(ctx, &plan, *want, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// specChanged tells whether the plan changes what UpdateSource files (the lock included).
func (r *sourceResource) specChanged(plan, st sourceModel) bool {
	same := []bool{plan.DisplayName.Equal(st.DisplayName), plan.Description.Equal(st.Description), plan.TrustDomain.Equal(st.TrustDomain),
		plan.SvidRegex.Equal(st.SvidRegex), plan.SampleSpiffeIDs.Equal(st.SampleSpiffeIDs), plan.Labels.Equal(st.Labels),
		plan.Visibility.Equal(st.Visibility), plan.VisibleTo.Equal(st.VisibleTo), plan.Managed.Equal(st.Managed)}
	return slices.Contains(same, false)
}

func (r *sourceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st sourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
		return
	}
	name, rev := st.Name.ValueString(), st.Revision.ValueInt64()
	if _, err := r.session.apply(ctx, filing{kind: "SOURCE", name: name,
		file: func(ctx context.Context) (adminclient.ChangeRequest, error) {
			return r.session.Client.RetireSource(ctx, name, rev, r.session.options(nil)) // the owner retires without saying managed
		}}); err != nil {
		resp.Diagnostics.AddError("Cannot retire the source", err.Error())
	}
}

func (r *sourceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ModifyPlan refuses what ligo would refuse, before the apply (spec §3.7).
func (r *sourceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.session == nil { // a destroy, or no provider yet
		return
	}
	var plan sourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	changes := req.State.Raw.IsNull() || !req.State.Raw.Equal(req.Plan.Raw)
	if !changes {
		return
	}
	if plan.effectiveManaged(r.session) && r.session.Person {
		resp.Diagnostics.AddAttributeError(path.Root("managed"), "A person cannot own a resource",
			"The provider authenticates with username, so it is a person, and ligo refuses managed resources from a person. "+
				"Set managed = false on the resource (or managed = false in the provider), or authenticate with a SPIFFE identity.")
	}
	if !req.State.Raw.IsNull() {
		var st sourceModel
		if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
			return
		}
		if owner := st.ManagedBy.ValueString(); owner != "" && r.session.CallerID != "" && owner != r.session.CallerID {
			resp.Diagnostics.AddError("Managed by another automation",
				fmt.Sprintf("The source %s is managed by %s, not by this caller (%s). Two pipelines never change one resource: change it from its owner, "+
					"or have a person release it.", st.Name.ValueString(), owner, r.session.CallerID))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	f := filing{kind: "SOURCE", name: plan.Name.ValueString(), payload: plan.body()}
	if cr, found, same, err := r.session.pending(ctx, f); err == nil && found {
		msg := fmt.Sprintf("Change request %s is pending for the source, awaiting %s.", cr.Id, awaiting(cr))
		if same {
			msg += " It asks for this very change: the apply waits for it and does not file another."
		} else {
			msg += " It asks for another change: the apply fails until a person decides it."
		}
		resp.Diagnostics.AddWarning("A change request is pending", msg)
	}
}

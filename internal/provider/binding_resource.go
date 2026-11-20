package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dihedron/ligo/pkg/adminclient"
	"github.com/dihedron/terraform-provider-ligo/internal/jsonnorm"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// bindingResource is ligo_binding (spec §3.2): the access a source has to a
// target. It never lifts a hold, and never retries or rotates a binding: those
// are people's acts.
type bindingResource struct{ session *Session }

var (
	_ resource.Resource                = (*bindingResource)(nil)
	_ resource.ResourceWithConfigure   = (*bindingResource)(nil)
	_ resource.ResourceWithImportState = (*bindingResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*bindingResource)(nil)
)

func newBindingResource() resource.Resource { return &bindingResource{} }

type bindingModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Source            types.String `tfsdk:"source"`
	Target            types.String `tfsdk:"target"`
	AccessLevel       types.String `tfsdk:"access_level"`
	Parameters        types.String `tfsdk:"parameters"`
	Description       types.String `tfsdk:"description"`
	ExpiresAt         types.String `tfsdk:"expires_at"`
	Annotations       types.Map    `tfsdk:"annotations"`
	Managed           types.Bool   `tfsdk:"managed"`
	Revision          types.Int64  `tfsdk:"revision"`
	ManagedBy         types.String `tfsdk:"managed_by"`
	LastChangeRequest types.String `tfsdk:"last_change_request"`
	Phase             types.String `tfsdk:"phase"`
	PhaseMessage      types.String `tfsdk:"phase_message"`
}

func (r *bindingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_binding"
}

func (r *bindingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A binding: the access that a source has to a target. Every change is a change request that the provider files once and waits for, " +
			"and then waits until the binding is ACTIVE; it never approves a request, never lifts a hold and never retries a failed binding. " +
			"Removal revokes the access. The parameters name credentials by reference, never by value.",
		Attributes: map[string]schema.Attribute{
			"id":           schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "The name."},
			"name":         schema.StringAttribute{Required: true, PlanModifiers: replace, Description: "The name; a new name is a new resource."},
			"source":       schema.StringAttribute{Required: true, PlanModifiers: replace, Description: "The source that is given the access; a binding does not move to another."},
			"target":       schema.StringAttribute{Required: true, PlanModifiers: replace, Description: "The target that is reached; a binding does not move to another."},
			"access_level": schema.StringAttribute{Required: true, Description: "The access level of the target that is granted."},
			"parameters": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), sameJSON{}},
				Description: "The parameters of the access, as JSON text (jsonencode); the platform's form says which. They are checked against it when the plan can read it. " +
					"When the configuration has none, they are what the platform defaults to, as ligo answers them."},
			"description": schema.StringAttribute{Optional: true, Description: "What the binding is for, in plain text; ligo sends it to the workload."},
			"expires_at":  schema.StringAttribute{Optional: true, Description: "When the binding expires, as an RFC 3339 time."},
			"annotations": schema.MapAttribute{Optional: true, ElementType: types.StringType, Description: "Free-form key-value text, not interpreted by ligo."},
			"managed": schema.BoolAttribute{Optional: true, Description: "Whether this automation owns it (the lock): only the owner then changes it. Default: the provider's managed. " +
				"false, on one that is managed, is the owner's release."},
			"revision":   schema.Int64Attribute{Computed: true, Description: "The revision; an update names it, and ligo refuses a stale one."},
			"managed_by": schema.StringAttribute{Computed: true, Description: "The SPIFFE ID of the automation that owns it; null when nobody does."},
			"last_change_request": schema.StringAttribute{Computed: true,
				Description: "The ID of the last change request the provider filed or waited for."},
			"phase":         schema.StringAttribute{Computed: true, Description: "PROVISIONING, ACTIVE, FAILED, DELETING or DELETE_FAILED."},
			"phase_message": schema.StringAttribute{Computed: true, Description: "The last message of the workflow that provisions the binding."},
		},
	}
}

func (r *bindingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (m bindingModel) effectiveManaged(s *Session) bool {
	if m.Managed.IsNull() || m.Managed.IsUnknown() {
		return s.DefaultManaged
	}
	return m.Managed.ValueBool()
}

// instant is an RFC 3339 text as the time it names, UTC.
func instant(s types.String) *string {
	v := optStr(s)
	if v == nil || *v == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, *v); err == nil {
		out := t.UTC().Format(time.RFC3339Nano)
		return &out
	}
	return v
}

// body is the spec as a request carries it: also what a pending request of
// ours is recognised by, and what the typed spec is decoded from.
func (m bindingModel) body() map[string]any {
	b := map[string]any{"name": m.Name.ValueString(), "source": m.Source.ValueString(), "target": m.Target.ValueString(),
		"accessLevel": m.AccessLevel.ValueString()}
	if v := jsonOrNil(m.Parameters); v != nil {
		b["parameters"] = v
	}
	if v := optStr(m.Description); v != nil {
		b["description"] = *v
	}
	if v := instant(m.ExpiresAt); v != nil {
		b["expiresAt"] = *v
	}
	if l := strMap(m.Annotations); l != nil {
		b["annotations"] = *l
	}
	return b
}

func (m bindingModel) spec() (adminclient.BindingSpec, error) {
	var spec adminclient.BindingSpec
	raw, err := json.Marshal(m.body())
	if err == nil {
		err = json.Unmarshal(raw, &spec)
	}
	return spec, err
}

// fill sets what ligo holds, keeping the user's text where it means the same.
func (m *bindingModel) fill(b adminclient.BindingDetail) error {
	m.ID, m.Name = types.StringValue(b.Metadata.Name), types.StringValue(b.Metadata.Name)
	m.Source, m.Target, m.AccessLevel = types.StringValue(b.Source), types.StringValue(b.Target), types.StringValue(b.AccessLevel)
	m.Revision, m.ManagedBy = types.Int64Value(b.Metadata.Revision), types.StringPointerValue(b.Metadata.ManagedBy)
	m.Phase, m.PhaseMessage = types.StringValue(string(b.Status.Phase)), types.StringValue(b.Status.Message)
	m.Description = types.StringPointerValue(b.Description)
	m.Annotations = annotationsOrNull(b.Annotations, m.Annotations)
	parameters, expires := m.Parameters, m.ExpiresAt
	m.Parameters, m.ExpiresAt = types.StringNull(), types.StringNull()
	if p, ok := b.Parameters.(map[string]any); ok && len(p) > 0 {
		text, err := jsonnorm.Of(p)
		if err != nil {
			return fmt.Errorf("parameters: %w", err)
		}
		m.Parameters = keepIfSame(parameters, text)
	}
	if b.ExpiresAt != nil {
		m.ExpiresAt = types.StringValue(b.ExpiresAt.UTC().Format(time.RFC3339))
		if t, err := time.Parse(time.RFC3339, expires.ValueString()); err == nil && t.Equal(*b.ExpiresAt) {
			m.ExpiresAt = expires // the user's own notation of the same instant
		}
	}
	return nil
}

func (r *bindingResource) readBack(ctx context.Context, m *bindingModel, cr string, diags *diag.Diagnostics) bool {
	b, err := paced(ctx, r.session, func(ctx context.Context) (adminclient.BindingDetail, error) {
		return r.session.Client.GetBinding(ctx, m.Name.ValueString())
	})
	if err == nil {
		err = m.fill(b)
	}
	if err != nil {
		diags.AddError("Cannot read the binding after the change", err.Error())
		return false
	}
	m.LastChangeRequest = types.StringValue(cr)
	return true
}

// failed says what a binding that ended FAILED needs: it exists in ligo, so the
// state holds it, and Terraform replaces what it marks as tainted.
func failed(what string, err error) string {
	return fmt.Sprintf("The change request was applied, but the binding did not come up: %s. The binding is in ligo and in the state as it is: "+
		"a person looks at its stages (ligo binding show %s); Terraform replaces it (terraform apply -replace) once the cause is fixed.", err, what)
}

func (r *bindingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bindingModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	managed := plan.effectiveManaged(r.session)
	spec, err := plan.spec()
	if err != nil {
		resp.Diagnostics.AddError("Cannot build the request", err.Error())
		return
	}
	cr, err := r.session.apply(ctx, filing{kind: "BINDING", name: plan.Name.ValueString(), payload: plan.body(), untilActive: true,
		file: func(ctx context.Context) (adminclient.ChangeRequest, error) {
			return r.session.Client.CreateBinding(ctx, spec, false, r.session.options(&managed))
		}})
	if err != nil {
		if errors.Is(err, adminclient.ErrBindingFailed) && r.readBack(ctx, &plan, cr.Id.String(), &resp.Diagnostics) {
			resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
			resp.Diagnostics.AddError("The binding failed", failed(plan.Name.ValueString(), err))
			return
		}
		resp.Diagnostics.AddError("Cannot create the binding", err.Error())
		return
	}
	if r.readBack(ctx, &plan, cr.Id.String(), &resp.Diagnostics) {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *bindingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st bindingModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
		return
	}
	b, err := paced(ctx, r.session, func(ctx context.Context) (adminclient.BindingDetail, error) {
		return r.session.Client.GetBinding(ctx, st.Name.ValueString())
	})
	if errors.Is(err, adminclient.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		readError(&resp.Diagnostics, "ligo_binding "+st.Name.ValueString(), err)
		return
	}
	if err := st.fill(b); err != nil {
		resp.Diagnostics.AddError("Cannot read the binding", err.Error())
		return
	}
	if bg := b.Status.BreakGlass; bg != nil {
		resp.Diagnostics.AddWarning("A break-glass change is open", fmt.Sprintf("Binding %s was changed in an emergency (change request %s, incident %s, by %s, until %s). "+
			"What is shown is what ligo holds; ligo refuses an apply until a person has reviewed it.", st.Name.ValueString(), bg.ChangeRequestId, bg.IncidentReference, bg.RequestedBy, bg.ExpiresAt.Format(time.RFC3339)))
	}
	if b.Status.Phase == "FAILED" || b.Status.Phase == "DELETE_FAILED" {
		resp.Diagnostics.AddWarning("The binding is "+string(b.Status.Phase), fmt.Sprintf("Binding %s: %s. A person looks at its stages; Terraform replaces it with terraform apply -replace once the cause is fixed.",
			st.Name.ValueString(), b.Status.Message))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, st)...)
}

func (r *bindingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, st bindingModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
		return
	}
	managed := plan.effectiveManaged(r.session)
	spec, err := plan.spec()
	if err != nil {
		resp.Diagnostics.AddError("Cannot build the request", err.Error())
		return
	}
	cr, err := r.session.apply(ctx, filing{kind: "BINDING", name: plan.Name.ValueString(), payload: plan.body(), untilActive: true,
		file: func(ctx context.Context) (adminclient.ChangeRequest, error) {
			return r.session.Client.UpdateBinding(ctx, plan.Name.ValueString(), spec, st.Revision.ValueInt64(), r.session.options(&managed))
		}})
	if err != nil {
		if errors.Is(err, adminclient.ErrBindingFailed) && r.readBack(ctx, &plan, cr.Id.String(), &resp.Diagnostics) {
			resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
			resp.Diagnostics.AddError("The binding failed", failed(plan.Name.ValueString(), err))
			return
		}
		resp.Diagnostics.AddError("Cannot update the binding", err.Error())
		return
	}
	if r.readBack(ctx, &plan, cr.Id.String(), &resp.Diagnostics) {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

// bindingPoll is the pause between two reads of a binding that is being
// revoked; a variable for the tests.
var bindingPoll = time.Second

// Delete revokes the access: the change request, then the binding's own
// teardown, which is waited for until the binding is gone.
func (r *bindingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st bindingModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
		return
	}
	name, rev := st.Name.ValueString(), st.Revision.ValueInt64()
	if _, err := r.session.stateChange(ctx, "BINDING", name, func(ctx context.Context) (adminclient.ChangeRequest, error) {
		return r.session.Client.DeleteBinding(ctx, name, rev, r.session.options(nil))
	}); err != nil {
		resp.Diagnostics.AddError("Cannot revoke the binding", err.Error())
		return
	}
	waitCtx, cancel := context.WithTimeout(ctx, r.session.ApprovalTimeout)
	defer cancel()
	phase := "not read"
	for {
		b, err := r.session.Client.GetBinding(waitCtx, name)
		switch {
		case errors.Is(err, adminclient.ErrNotFound):
			return
		case err != nil && waitCtx.Err() == nil:
			resp.Diagnostics.AddError("Cannot see the binding being revoked", err.Error())
			return
		case err == nil && b.Status.Phase == "DELETE_FAILED":
			resp.Diagnostics.AddError("The revocation failed", fmt.Sprintf("Binding %s ended DELETE_FAILED: %s. A person looks at its stages (ligo binding show %s); the apply may be run again.", name, b.Status.Message, name))
			return
		case err == nil:
			phase = string(b.Status.Phase)
		}
		select {
		case <-waitCtx.Done():
			resp.Diagnostics.AddError("The binding is still being revoked", fmt.Sprintf("Binding %s is %s after %s; it stays in ligo and goes on. Run the apply again to see it gone.", name, phase, r.session.ApprovalTimeout))
			return
		case <-time.After(bindingPoll):
		}
	}
}

func (r *bindingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ModifyPlan refuses what ligo would refuse, and what the platform's form
// refuses, before any filing.
func (r *bindingResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.session == nil {
		return
	}
	var plan, st bindingModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	if !req.State.Raw.IsNull() {
		if resp.Diagnostics.Append(req.State.Get(ctx, &st)...); resp.Diagnostics.HasError() {
			return
		}
		// A JSON text that was only reformatted makes the framework mark what ligo
		// computes as unknown: when nothing else differs, the plan is the state.
		same := plan
		same.Revision, same.ManagedBy, same.LastChangeRequest, same.Phase, same.PhaseMessage = st.Revision, st.ManagedBy, st.LastChangeRequest, st.Phase, st.PhaseMessage
		np := tfsdk.Plan{Schema: req.Plan.Schema}
		if !np.Set(ctx, same).HasError() && np.Raw.Equal(req.State.Raw) {
			resp.Plan = np
			return
		}
	}
	changes := req.State.Raw.IsNull() || !req.State.Raw.Equal(resp.Plan.Raw)
	r.session.lockChecks(ctx, "BINDING", plan.Name.ValueString(), plan.effectiveManaged(r.session), st.ManagedBy, !req.State.Raw.IsNull(), changes, plan.body(), &resp.Diagnostics)
	if changes && !resp.Diagnostics.HasError() {
		r.validate(ctx, plan, &resp.Diagnostics)
	}
}

// validate checks the parameters against the platform's binding form. A target
// that does not exist yet (it is made by this very configuration) or a platform
// that cannot be read is no reason to refuse: ligo checks anyway.
func (r *bindingResource) validate(ctx context.Context, plan bindingModel, diags *diag.Diagnostics) {
	if plan.Target.IsUnknown() || plan.Target.IsNull() || plan.Parameters.IsUnknown() || plan.Parameters.IsNull() {
		return
	}
	vals, ok := jsonOrNil(plan.Parameters).(map[string]any)
	if !ok {
		diags.AddAttributeError(path.Root("parameters"), "Not a JSON object", "It must be a JSON object, as jsonencode({...}) makes.")
		return
	}
	t, err := r.session.Client.GetTarget(ctx, plan.Target.ValueString())
	if errors.Is(err, adminclient.ErrNotFound) {
		return
	}
	var forms map[string]any
	if err == nil {
		var p adminclient.Platform
		if p, err = r.session.Client.GetPlatform(ctx, t.Metadata.Platform); err == nil && p.UiSchemas != nil {
			forms = *p.UiSchemas
		}
	}
	if forms == nil {
		diags.AddWarning("Not validated at plan time", fmt.Sprintf("The binding form of the platform of target %s could not be read, so the parameters are checked by ligo at the apply.", plan.Target.ValueString()))
		return
	}
	f, _ := forms["bindingParameters"].(map[string]any)
	for _, problem := range validateForm(f, vals) {
		diags.AddAttributeError(path.Root("parameters"), "Not valid for the platform of the target", problem)
	}
}

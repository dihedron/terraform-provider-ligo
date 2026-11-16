package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/dihedron/ligo/pkg/adminclient"
	"github.com/dihedron/terraform-provider-ligo/internal/jsonnorm"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// An identity provider or a secret store instance (spec §3.2, ligo's
// .specs/ligo-instances.md): one resource type for the two, which have the same
// shape. A person decides every change to an instance, whatever the charter
// says, so an apply always waits; an instance is never managed (no lock); its
// config names a bootstrap credential by reference and is sensitive (T-P2,
// T-P10).

type instanceKind struct {
	typeName string // ligo_<typeName>
	defKind  string // the definition's kind
	crKind   string // the change requests' resource type
	what     string
	create   func(*adminclient.Client, context.Context, adminclient.Definition, adminclient.Options) (adminclient.ChangeRequest, error)
	update   func(*adminclient.Client, context.Context, string, adminclient.Definition, int64, adminclient.Options) (adminclient.ChangeRequest, error)
	retire   func(*adminclient.Client, context.Context, string, int64, adminclient.Options) (adminclient.ChangeRequest, error)
	get      func(*adminclient.Client, context.Context, string) (adminclient.Instance, error)
	model    func() instanceModeler      // a fresh model of the kind
	extra    map[string]schema.Attribute // what the kind has besides the shared attributes
}

// instanceModeler is what the shared resource needs of a model: the shared part,
// and the kind's own part of the request and of what ligo returns.
type instanceModeler interface {
	base() *instanceModel
	extraBody(map[string]any)
	fillExtra(*adminclient.Definition)
}

func (m *instanceModel) base() *instanceModel              { return m }
func (m *instanceModel) extraBody(map[string]any)          {}
func (m *instanceModel) fillExtra(*adminclient.Definition) {}

var identityProviderKind = instanceKind{typeName: "identity_provider", defKind: "IdentityProvider", crKind: "IDENTITY_PROVIDER", what: "an identity provider instance: how the platforms know who a workload or a person is",
	create: (*adminclient.Client).CreateIdentityProvider, update: (*adminclient.Client).UpdateIdentityProvider,
	retire: (*adminclient.Client).RetireIdentityProvider, get: (*adminclient.Client).GetIdentityProvider,
	model: func() instanceModeler { return &instanceModel{} }}

var secretStoreKind = instanceKind{typeName: "secret_store", defKind: "SecretStore", crKind: "SECRET_STORE", what: "a secret store instance: where the platforms keep the credentials they hand out",
	create: (*adminclient.Client).CreateSecretStore, update: (*adminclient.Client).UpdateSecretStore,
	retire: (*adminclient.Client).RetireSecretStore, get: (*adminclient.Client).GetSecretStore,
	model: func() instanceModeler { return &instanceModel{} }}

type instanceResource struct {
	session *Session
	kind    instanceKind
}

var (
	_ resource.Resource                = (*instanceResource)(nil)
	_ resource.ResourceWithConfigure   = (*instanceResource)(nil)
	_ resource.ResourceWithImportState = (*instanceResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*instanceResource)(nil)
)

func newIdentityProviderResource() resource.Resource {
	return &instanceResource{kind: identityProviderKind}
}
func newSecretStoreResource() resource.Resource { return &instanceResource{kind: secretStoreKind} }

type instanceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Jurisdiction      types.String `tfsdk:"jurisdiction"`
	Realm             types.String `tfsdk:"realm"`
	Type              types.String `tfsdk:"type"`
	DisplayName       types.String `tfsdk:"display_name"`
	Description       types.String `tfsdk:"description"`
	Contacts          types.List   `tfsdk:"contacts"`
	SvidSelector      types.Object `tfsdk:"svid_selector"`
	Config            types.String `tfsdk:"config"`
	NetworkFlows      types.String `tfsdk:"network_flows"`
	Revision          types.Int64  `tfsdk:"revision"`
	LastChangeRequest types.String `tfsdk:"last_change_request"`
}

var (
	contactType  = types.ObjectType{AttrTypes: map[string]attr.Type{"name": types.StringType, "email": types.StringType}}
	selectorType = types.ObjectType{AttrTypes: map[string]attr.Type{"match_expr": types.StringType, "jwt_audience": types.ListType{ElemType: types.StringType}, "allow_shared_svid": types.BoolType}}
)

func (r *instanceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.kind.typeName
}

func (r *instanceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	attrs := map[string]schema.Attribute{
		"id":           schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "The name."},
		"name":         schema.StringAttribute{Required: true, PlanModifiers: replace, Description: "The name, unique among the instances of the kind; a new name is a new resource."},
		"jurisdiction": schema.StringAttribute{Required: true, PlanModifiers: replace, Description: "The jurisdiction that owns it; a person on it decides every change. It cannot change."},
		"realm":        schema.StringAttribute{Required: true, PlanModifiers: replace, Description: "The realm; it cannot change."},
		"type":         schema.StringAttribute{Required: true, PlanModifiers: replace, Description: "The plugin type and version, as server.yaml names it (such as vault.v1); it cannot change."},
		"display_name": schema.StringAttribute{Optional: true, Description: "A name for people."},
		"description":  schema.StringAttribute{Optional: true, Description: "What it is."},
		"contacts": schema.ListNestedAttribute{Optional: true, Description: "Who to ask.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"name":  schema.StringAttribute{Required: true, Description: "A name or a team."},
			"email": schema.StringAttribute{Optional: true, Description: "Their address."},
		}}},
		"svid_selector": schema.SingleNestedAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
			Description: "Which SVID is the plugin's, which the plugin process of the type has to present.",
			Attributes: map[string]schema.Attribute{
				"match_expr":        schema.StringAttribute{Required: true, Description: "The expression that selects the SVID."},
				"jwt_audience":      schema.ListAttribute{Optional: true, ElementType: types.StringType, Description: "The audiences of the JWT-SVID it sends to the bootstrap credential's store."},
				"allow_shared_svid": schema.BoolAttribute{Optional: true, Description: "Whether other instances of the type may share the SVID."},
			}},
		"config": schema.StringAttribute{Optional: true, Sensitive: true, PlanModifiers: []planmodifier.String{sameJSON{}},
			Description: "The plugin's configuration, as JSON text (jsonencode); some plugins (svid, none) have none. ligo checks it against the plugin's schema when the change is filed. " +
				"It names a bootstrap credential by reference (a store, a path), never by value."},
		"network_flows":       schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{sameJSON{}}, Description: "The network flows the instance needs, as JSON text."},
		"revision":            schema.Int64Attribute{Computed: true, Description: "The revision; an update names it, and ligo refuses a stale one."},
		"last_change_request": schema.StringAttribute{Computed: true, Description: "The ID of the last change request the provider filed or waited for."},
	}
	maps.Copy(attrs, r.kind.extra)
	resp.Schema = schema.Schema{
		Description: "A " + r.kind.what + ". A person decides every change to it, whatever the charter says: the provider files the change request once and waits for " +
			"it (approval_timeout in the provider), and never approves one. It is never managed (there is no lock). Its config names a credential by reference, never by value.",
		Attributes: attrs,
	}
}

func (r *instanceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// body is the definition as a request carries it: also what a pending request
// of ours is recognised by, and what the typed definition is decoded from.
func (m *instanceModel) body(kind string) map[string]any {
	meta := map[string]any{"name": m.Name.ValueString(), "jurisdiction": m.Jurisdiction.ValueString(), "realm": m.Realm.ValueString()}
	if v := optStr(m.DisplayName); v != nil {
		meta["displayName"] = *v
	}
	if v := optStr(m.Description); v != nil {
		meta["description"] = *v
	}
	if cs := m.contacts(); len(cs) > 0 {
		meta["contacts"] = cs
	}
	b := map[string]any{"kind": kind, "metadata": meta, "type": m.Type.ValueString()}
	if sel := m.selector(); sel != nil {
		b["svidSelector"] = sel
	}
	if v := jsonOrNil(m.Config); v != nil {
		b["config"] = v
	}
	if v := jsonOrNil(m.NetworkFlows); v != nil {
		b["networkFlows"] = v
	}
	return b
}

func (m *instanceModel) contacts() []any {
	var out []any
	for _, e := range m.Contacts.Elements() {
		a := e.(types.Object).Attributes()
		c := map[string]any{"name": a["name"].(types.String).ValueString()}
		if v := optStr(a["email"].(types.String)); v != nil {
			c["email"] = *v
		}
		out = append(out, c)
	}
	return out
}

func (m *instanceModel) selector() map[string]any {
	if m.SvidSelector.IsNull() || m.SvidSelector.IsUnknown() {
		return nil
	}
	a := m.SvidSelector.Attributes()
	sel := map[string]any{"matchExpr": a["match_expr"].(types.String).ValueString()}
	if l := strList(a["jwt_audience"].(types.List)); l != nil {
		sel["jwtAudience"] = *l
	}
	if b := a["allow_shared_svid"].(types.Bool); !b.IsNull() && !b.IsUnknown() {
		sel["allowSharedSvid"] = b.ValueBool()
	}
	return sel
}

// bodyOf is the whole request body: the shared part and the kind's own.
func (r *instanceResource) bodyOf(m instanceModeler) map[string]any {
	b := m.base().body(r.kind.defKind)
	m.extraBody(b)
	return b
}

// fill sets what ligo holds. What a caller who may not file cannot see (the
// definition, the revision) is kept as the state has it: a nil is not a null.
func (m *instanceModel) fill(i adminclient.Instance) error {
	m.ID, m.Name, m.Jurisdiction = types.StringValue(i.Name), types.StringValue(i.Name), types.StringValue(i.Jurisdiction)
	m.Realm, m.Type = types.StringValue(i.Realm), types.StringValue(i.Type)
	m.DisplayName, m.Description = types.StringPointerValue(i.DisplayName), types.StringPointerValue(i.Description)
	if i.Revision != nil {
		m.Revision = types.Int64Value(*i.Revision)
	}
	m.Contacts = types.ListNull(contactType)
	if i.Contacts != nil && len(*i.Contacts) > 0 {
		var cs []attr.Value
		for _, c := range *i.Contacts {
			cs = append(cs, types.ObjectValueMust(contactType.AttrTypes, map[string]attr.Value{"name": types.StringValue(c.Name), "email": types.StringPointerValue(c.Email)}))
		}
		m.Contacts = types.ListValueMust(contactType, cs)
	}
	prior := m.NetworkFlows
	m.NetworkFlows = types.StringNull()
	if len(i.NetworkFlows) > 0 {
		text, err := jsonnorm.Of(i.NetworkFlows)
		if err != nil {
			return fmt.Errorf("network_flows: %w", err)
		}
		m.NetworkFlows = keepIfSame(prior, text)
	}
	if d := i.Definition; d != nil {
		m.SvidSelector = types.ObjectNull(selectorType.AttrTypes)
		if sel := d.SvidSelector; sel != nil {
			aud := types.ListNull(types.StringType)
			if sel.JwtAudience != nil {
				aud = listOf(*sel.JwtAudience)
			}
			m.SvidSelector = types.ObjectValueMust(selectorType.AttrTypes, map[string]attr.Value{"match_expr": types.StringValue(sel.MatchExpr), "jwt_audience": aud,
				"allow_shared_svid": types.BoolPointerValue(sel.AllowSharedSvid)})
		}
		was := m.Config
		m.Config = types.StringNull() // a plugin without settings has none
		if d.Config != nil && len(*d.Config) > 0 {
			text, err := jsonnorm.Of(*d.Config)
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}
			m.Config = keepIfSame(was, text)
		}
	}
	return nil
}

func (r *instanceResource) fill(m instanceModeler, i adminclient.Instance) error {
	if err := m.base().fill(i); err != nil {
		return err
	}
	if i.Definition != nil {
		m.fillExtra(i.Definition)
	}
	return nil
}

// readBackWait is how long a change that was applied is read back while ligo
// says the instance is unavailable: a platform's plugin is started or
// reconfigured a moment after the approval, and a platform cannot be read
// until it serves. A variable for the tests.
var (
	readBackWait = time.Minute
	readBackStep = time.Second
)

func (r *instanceResource) readBack(ctx context.Context, m instanceModeler, cr string, diags *diag.Diagnostics) bool {
	b := m.base()
	deadline := time.Now().Add(readBackWait)
	var i adminclient.Instance
	var err error
	for {
		if i, err = r.kind.get(r.session.Client, ctx, b.Name.ValueString()); !errors.Is(err, adminclient.ErrUnavailable) || time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(readBackStep):
		}
	}
	if err == nil {
		err = r.fill(m, i)
	}
	if err != nil {
		diags.AddError("Cannot read the "+strings.ReplaceAll(r.kind.typeName, "_", " ")+" after the change", err.Error())
		return false
	}
	b.LastChangeRequest = types.StringValue(cr)
	return true
}

func (r *instanceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	plan := r.kind.model()
	if resp.Diagnostics.Append(req.Plan.Get(ctx, plan)...); resp.Diagnostics.HasError() {
		return
	}
	body := r.bodyOf(plan)
	def, err := decodeDefinition(body)
	if err != nil {
		resp.Diagnostics.AddError("Cannot build the request", err.Error())
		return
	}
	cr, err := r.session.apply(ctx, filing{kind: r.kind.crKind, name: plan.base().Name.ValueString(), payload: body,
		file: func(ctx context.Context) (adminclient.ChangeRequest, error) {
			return r.kind.create(r.session.Client, ctx, def, r.session.options(nil))
		}})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the "+r.kind.typeName, err.Error())
		return
	}
	if r.readBack(ctx, plan, cr.Id.String(), &resp.Diagnostics) {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *instanceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	st := r.kind.model()
	if resp.Diagnostics.Append(req.State.Get(ctx, st)...); resp.Diagnostics.HasError() {
		return
	}
	name := st.base().Name.ValueString()
	i, err := r.kind.get(r.session.Client, ctx, name)
	if errors.Is(err, adminclient.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		readError(&resp.Diagnostics, "ligo_"+r.kind.typeName+" "+name, err)
		return
	}
	if i.Quarantined != nil && *i.Quarantined {
		resp.Diagnostics.AddWarning("The instance is quarantined", fmt.Sprintf("ligo isolated %s %s: its stored row does not verify, so it is not in force. "+
			"The state is left as it is; a person looks at it, and a normal change request fixes it.", r.kind.typeName, name))
		resp.Diagnostics.Append(resp.State.Set(ctx, st)...)
		return
	}
	if i.Definition == nil || i.Revision == nil {
		resp.Diagnostics.AddWarning("The definition cannot be read", fmt.Sprintf("The caller may not file on the jurisdiction of %s %s, so ligo does not show its definition: "+
			"the state keeps its config and its revision, and drift in them is not seen.", r.kind.typeName, name))
	}
	if err := r.fill(st, i); err != nil {
		resp.Diagnostics.AddError("Cannot read the "+r.kind.typeName, err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, st)...)
}

func (r *instanceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	plan, st := r.kind.model(), r.kind.model()
	if resp.Diagnostics.Append(req.Plan.Get(ctx, plan)...); resp.Diagnostics.HasError() {
		return
	}
	if resp.Diagnostics.Append(req.State.Get(ctx, st)...); resp.Diagnostics.HasError() {
		return
	}
	body := r.bodyOf(plan)
	def, err := decodeDefinition(body)
	if err != nil {
		resp.Diagnostics.AddError("Cannot build the request", err.Error())
		return
	}
	name, rev := plan.base().Name.ValueString(), st.base().Revision.ValueInt64()
	cr, err := r.session.apply(ctx, filing{kind: r.kind.crKind, name: name, payload: body,
		file: func(ctx context.Context) (adminclient.ChangeRequest, error) {
			return r.kind.update(r.session.Client, ctx, name, def, rev, r.session.options(nil))
		}})
	if err != nil {
		resp.Diagnostics.AddError("Cannot update the "+r.kind.typeName, err.Error())
		return
	}
	if r.readBack(ctx, plan, cr.Id.String(), &resp.Diagnostics) {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *instanceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	st := r.kind.model()
	if resp.Diagnostics.Append(req.State.Get(ctx, st)...); resp.Diagnostics.HasError() {
		return
	}
	name, rev := st.base().Name.ValueString(), st.base().Revision.ValueInt64()
	if _, err := r.session.stateChange(ctx, r.kind.crKind, name, func(ctx context.Context) (adminclient.ChangeRequest, error) {
		return r.kind.retire(r.session.Client, ctx, name, rev, r.session.options(nil))
	}); err != nil {
		resp.Diagnostics.AddError("Cannot retire the "+r.kind.typeName, err.Error())
	}
}

func (r *instanceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ModifyPlan: a reformatting is no change, and a request that is still open is
// told. Nothing else is checked here: ligo checks the config against the
// plugin's schema when the change is filed, and the provider has no way to
// read that schema; it checks the wiring of a platform at the approval.
func (r *instanceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.session == nil {
		return
	}
	plan, st := r.kind.model(), r.kind.model()
	if resp.Diagnostics.Append(req.Plan.Get(ctx, plan)...); resp.Diagnostics.HasError() {
		return
	}
	body := r.bodyOf(plan)
	if !req.State.Raw.IsNull() {
		if resp.Diagnostics.Append(req.State.Get(ctx, st)...); resp.Diagnostics.HasError() {
			return
		}
		// the framework marks what ligo computes as unknown when a JSON text was only reformatted
		plan.base().Revision, plan.base().LastChangeRequest = st.base().Revision, st.base().LastChangeRequest
		np := tfsdk.Plan{Schema: req.Plan.Schema}
		if !np.Set(ctx, plan).HasError() && np.Raw.Equal(req.State.Raw) {
			resp.Plan = np
			return
		}
	}
	if req.State.Raw.IsNull() || !req.State.Raw.Equal(resp.Plan.Raw) {
		r.session.warnPending(ctx, r.kind.crKind, plan.base().Name.ValueString(), body, &resp.Diagnostics)
	}
}

// decodeDefinition turns the request body into the client's typed definition
// (the nested types are not exported).
func decodeDefinition(body map[string]any) (adminclient.Definition, error) {
	var def adminclient.Definition
	raw, err := json.Marshal(body)
	if err == nil {
		err = json.Unmarshal(raw, &def)
	}
	return def, err
}

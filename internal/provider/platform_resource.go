package provider

import (
	"context"

	"github.com/dihedron/ligo/pkg/adminclient"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// A platform (spec §3.2): the shared resource of the instances, with what only
// a platform has. It wires an identity provider and a secret store instance,
// which must be ACTIVE when a person approves the change (ligo checks it then);
// a platform in the same configuration is created after the instances it names.

var platformKind = instanceKind{typeName: "platform", defKind: "Platform", crKind: "PLATFORM",
	what:   "a platform: a kind of system that targets live on, served by a plugin, with the identity provider and the secret store that it uses",
	create: (*adminclient.Client).CreatePlatform, update: (*adminclient.Client).UpdatePlatform, retire: (*adminclient.Client).RetirePlatform,
	get: func(c *adminclient.Client, ctx context.Context, name string) (adminclient.Instance, error) {
		p, err := c.GetPlatform(ctx, name)
		if err != nil {
			return adminclient.Instance{}, err
		}
		return platformAsInstance(p), nil
	},
	model: func() instanceModeler { return &platformModel{} },
	extra: platformAttributes(),
}

func newPlatformResource() resource.Resource { return &instanceResource{kind: platformKind} }

// platformAsInstance is a platform in the shape the shared resource reads: the
// parts that a platform lists apart from its definition go back into it.
func platformAsInstance(p adminclient.Platform) adminclient.Instance {
	i := adminclient.Instance{ApiVersion: p.ApiVersion, Contacts: p.Contacts, Definition: p.Definition, Description: p.Description, DisplayName: p.DisplayName,
		Jurisdiction: p.Jurisdiction, Name: p.Name, NetworkFlows: p.NetworkFlows, Quarantined: p.Quarantined, Realm: p.Realm, Revision: p.Revision, Type: p.Type}
	if d := p.Definition; d != nil {
		def := *d
		if def.TargetJurisdictions == nil {
			def.TargetJurisdictions = p.TargetJurisdictions
		}
		if def.AllowedSourceTrustDomains == nil && len(p.AllowedSourceTrustDomains) > 0 {
			def.AllowedSourceTrustDomains = &p.AllowedSourceTrustDomains
		}
		i.Definition = &def
	}
	return i
}

type platformModel struct {
	instanceModel
	TargetJurisdictions       types.List   `tfsdk:"target_jurisdictions"`
	NamespaceTemplate         types.String `tfsdk:"namespace_template"`
	CheckExistence            types.Bool   `tfsdk:"check_existence"`
	PrincipalPrefix           types.String `tfsdk:"principal_prefix"`
	AllowedSourceTrustDomains types.List   `tfsdk:"allowed_source_trust_domains"`
	RotationInterval          types.String `tfsdk:"rotation_interval"`
	IdentityProvider          types.Object `tfsdk:"identity_provider"`
	SecretStore               types.Object `tfsdk:"secret_store"`
}

var (
	idpWiringType   = types.ObjectType{AttrTypes: map[string]attr.Type{"type": types.StringType, "instance": types.StringType}}
	storeWiringType = types.ObjectType{AttrTypes: map[string]attr.Type{"type": types.StringType, "instance": types.StringType, "secret_storage_strategy": types.StringType}}
)

func platformAttributes() map[string]schema.Attribute {
	keepList := []planmodifier.List{listplanmodifier.UseStateForUnknown()}
	keepStr := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	return map[string]schema.Attribute{
		"target_jurisdictions": schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType, PlanModifiers: keepList,
			Description: "The jurisdiction patterns its targets may have. Default: its own jurisdiction and everything under it."},
		"namespace_template": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keepStr,
			Description: "How the platform names what it creates for an account and a tenancy, such as {account}_{tenancy}."},
		"check_existence": schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			Description: "Whether the platform checks that a target exists before it is bound. Default: true."},
		"principal_prefix": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keepStr, Description: "A prefix of the principals the platform creates, such as svc_."},
		"allowed_source_trust_domains": schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType, PlanModifiers: keepList,
			Description: "The trust domains of the sources that may use the platform."},
		"rotation_interval": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keepStr, Description: "How often the platform rotates credentials, such as 24h."},
		"identity_provider": schema.SingleNestedAttribute{Required: true, Description: "The identity provider instance it uses; ligo checks at the approval that it is ACTIVE and in the same realm.",
			Attributes: map[string]schema.Attribute{
				"type":     schema.StringAttribute{Required: true, Description: "Its plugin type."},
				"instance": schema.StringAttribute{Required: true, Description: "Its name."},
			}},
		"secret_store": schema.SingleNestedAttribute{Required: true, Description: "The secret store instance it uses; ligo checks at the approval that it is ACTIVE and in the same realm.",
			Attributes: map[string]schema.Attribute{
				"type":                    schema.StringAttribute{Required: true, Description: "Its plugin type."},
				"instance":                schema.StringAttribute{Required: true, Description: "Its name."},
				"secret_storage_strategy": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keepStr, Description: "How the platform keeps the credentials in the store; ligo defaults it when the store declares one."},
			}},
	}
}

// extraBody adds what only a platform has to the definition.
func (m *platformModel) extraBody(b map[string]any) {
	set := func(key string, v any) {
		if v != nil {
			b[key] = v
		}
	}
	str := func(s types.String) any {
		if v := optStr(s); v != nil {
			return *v
		}
		return nil
	}
	list := func(l types.List) any {
		if v := strList(l); v != nil {
			return *v
		}
		return nil
	}
	set("targetJurisdictions", list(m.TargetJurisdictions))
	set("namespaceTemplate", str(m.NamespaceTemplate))
	if !m.CheckExistence.IsNull() && !m.CheckExistence.IsUnknown() {
		b["checkExistence"] = m.CheckExistence.ValueBool()
	}
	set("principalPrefix", str(m.PrincipalPrefix))
	set("allowedSourceTrustDomains", list(m.AllowedSourceTrustDomains))
	set("rotationInterval", str(m.RotationInterval))
	if !m.IdentityProvider.IsNull() && !m.IdentityProvider.IsUnknown() {
		a := m.IdentityProvider.Attributes()
		b["identityProvider"] = map[string]any{"type": a["type"].(types.String).ValueString(), "instance": a["instance"].(types.String).ValueString()}
	}
	if !m.SecretStore.IsNull() && !m.SecretStore.IsUnknown() {
		a := m.SecretStore.Attributes()
		w := map[string]any{"type": a["type"].(types.String).ValueString(), "instance": a["instance"].(types.String).ValueString()}
		if v := optStr(a["secret_storage_strategy"].(types.String)); v != nil {
			w["secretStorageStrategy"] = *v
		}
		b["secretStore"] = w
	}
}

// fillExtra sets what ligo holds of a platform's own settings.
func (m *platformModel) fillExtra(d *adminclient.Definition) {
	list := func(l *[]string) types.List {
		if l == nil {
			return types.ListNull(types.StringType)
		}
		return listOf(*l)
	}
	m.TargetJurisdictions, m.AllowedSourceTrustDomains = list(d.TargetJurisdictions), list(d.AllowedSourceTrustDomains)
	m.NamespaceTemplate, m.PrincipalPrefix, m.RotationInterval = types.StringPointerValue(d.NamespaceTemplate), types.StringPointerValue(d.PrincipalPrefix), types.StringPointerValue(d.RotationInterval)
	m.CheckExistence = types.BoolPointerValue(d.CheckExistence)
	if w := d.IdentityProvider; w != nil {
		m.IdentityProvider = types.ObjectValueMust(idpWiringType.AttrTypes, map[string]attr.Value{"type": types.StringValue(w.Type), "instance": types.StringValue(w.Instance)})
	}
	if w := d.SecretStore; w != nil {
		m.SecretStore = types.ObjectValueMust(storeWiringType.AttrTypes, map[string]attr.Value{"type": types.StringValue(w.Type), "instance": types.StringValue(w.Instance),
			"secret_storage_strategy": types.StringPointerValue(w.SecretStorageStrategy)})
	}
}

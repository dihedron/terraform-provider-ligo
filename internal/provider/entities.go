package provider

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/dihedron/ligo/pkg/adminclient"
	"github.com/dihedron/terraform-provider-ligo/internal/jsonnorm"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// --- attribute and value helpers -------------------------------------------

func strA(desc string) schema.Attribute {
	return schema.StringAttribute{Computed: true, Description: desc}
}
func intA(desc string) schema.Attribute {
	return schema.Int64Attribute{Computed: true, Description: desc}
}
func boolA(desc string) schema.Attribute {
	return schema.BoolAttribute{Computed: true, Description: desc}
}
func mapA(desc string) schema.Attribute {
	return schema.MapAttribute{Computed: true, ElementType: types.StringType, Description: desc}
}
func listA(desc string) schema.Attribute {
	return schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: desc}
}

// jsonA is a free-form part, as the JSON text of its normal form (spec §3.5).
func jsonA(desc string) schema.Attribute {
	return schema.StringAttribute{Computed: true, Description: desc + " As JSON text, in its normal form (sorted keys, no extra white space)."}
}

// jsonSecretA is a free-form part that might carry a secret by mistake: it is
// sensitive, so that no plan or output shows it (T-P10).
func jsonSecretA(desc string) schema.Attribute {
	return schema.StringAttribute{Computed: true, Sensitive: true, Description: desc + " Sensitive, as JSON text in its normal form (sorted keys, no extra white space)."}
}

func sv(s string) attr.Value { return types.StringValue(s) }
func sp(s *string) attr.Value {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}
func iv(n int64) attr.Value { return types.Int64Value(n) }
func bv(b bool) attr.Value  { return types.BoolValue(b) }
func tv(t *time.Time) attr.Value {
	if t == nil {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339))
}

// ptrID is a pointer to an id as a Stringer that is nil when the pointer is.
func ptrID[T fmt.Stringer](p *T) fmt.Stringer {
	if p == nil {
		return nil
	}
	return *p
}

func stv(id fmt.Stringer) attr.Value {
	if id == nil {
		return types.StringNull()
	}
	return types.StringValue(id.String())
}

func labelsV(m *map[string]string) attr.Value {
	if m == nil {
		return types.MapNull(types.StringType)
	}
	out := make(map[string]attr.Value, len(*m))
	for k, v := range *m {
		out[k] = types.StringValue(v)
	}
	return types.MapValueMust(types.StringType, out)
}

func strsV(l []string) attr.Value {
	out := make([]attr.Value, len(l))
	for i, s := range l {
		out[i] = types.StringValue(s)
	}
	return types.ListValueMust(types.StringType, out)
}

// jsonV is the normal form of a decoded value; an error names the attribute.
func jsonV(what string, v any) (attr.Value, error) {
	s, err := jsonnorm.Of(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if s == "" {
		return types.StringNull(), nil
	}
	return types.StringValue(s), nil
}

// pages turns a sequence of items into a sequence of mapped items.
func pages[T any](seq iter.Seq2[T, error], f func(T) (vals, error)) iter.Seq2[vals, error] {
	return func(yield func(vals, error) bool) {
		for it, err := range seq {
			if err != nil {
				yield(nil, err)
				return
			}
			v, err := f(it)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(v, nil) {
				return
			}
		}
	}
}

// --- the entities ----------------------------------------------------------

func commonFilters() map[string]filterAttr {
	return map[string]filterAttr{
		"jurisdiction": {desc: "Only what lies in this jurisdiction pattern."},
		"state":        {desc: "ACTIVE, DEPRECATED or RETIRED; the default is every state but RETIRED."},
		"label":        {desc: "A label, key=value."},
		"q":            {desc: "Matched in names, display names and descriptions."},
	}
}

func sourceVals(m adminclient.Source) (vals, error) {
	return vals{
		"name": sv(m.Metadata.Name), "display_name": sp(m.DisplayName), "description": sp(m.Description),
		"trust_domain": sv(m.Identity.TrustDomain), "svid_regex": sv(m.Identity.SvidRegex),
		"jurisdiction": sv(m.Metadata.Jurisdiction), "state": sv(string(m.Metadata.State)), "revision": iv(m.Metadata.Revision),
		"managed_by": sp(m.Metadata.ManagedBy), "labels": labelsV(m.Labels), "visibility": sv(string(m.Visibility.Mode)),
		"bindings": iv(int64(m.Status.Bindings)), "pending_change_request_id": stv(ptrID(m.Metadata.PendingChangeRequestId)),
	}, nil
}

var sourceItem = map[string]schema.Attribute{
	"name": strA("The name."), "display_name": strA("A name for people."), "description": strA("What it is."),
	"trust_domain": strA("The trust domain of the SPIFFE IDs it matches."), "svid_regex": strA("The SPIFFE ID regex."),
	"jurisdiction": strA("The jurisdiction, derived from the regex."), "state": strA("ACTIVE, DEPRECATED or RETIRED."),
	"revision": intA("The revision; an update must name it."), "managed_by": strA("The SPIFFE ID of the automation that manages it (the lock); empty when nobody does."),
	"labels": mapA("Labels."), "visibility": strA("Who may see it in the catalog: the visibility mode."), "bindings": intA("How many bindings use it."),
	"pending_change_request_id": strA("A change request of it that is still pending, if any."),
}

func targetVals(m adminclient.Target) (vals, error) {
	def, err := jsonV("definition", m.Definition)
	if err != nil {
		return nil, err
	}
	lv, err := jsonV("access_levels", m.AccessLevels)
	if err != nil {
		return nil, err
	}
	return vals{
		"name": sv(m.Metadata.Name), "display_name": sp(m.DisplayName), "description": sp(m.Description),
		"platform": sv(m.Metadata.Platform), "platform_type": sv(m.Metadata.PlatformType), "realm": sv(m.Metadata.Realm),
		"jurisdiction": sv(m.Metadata.Jurisdiction), "platform_jurisdiction": sv(m.Metadata.PlatformJurisdiction),
		"state": sv(string(m.Metadata.State)), "revision": iv(m.Metadata.Revision), "managed_by": sp(m.Metadata.ManagedBy),
		"labels": labelsV(m.Labels), "visibility": sv(string(m.Visibility.Mode)), "definition": def, "access_levels": lv,
		"bindings": iv(int64(m.Status.Bindings)), "pending_change_request_id": stv(ptrID(m.Metadata.PendingChangeRequestId)),
	}, nil
}

var targetItem = map[string]schema.Attribute{
	"name": strA("The name."), "display_name": strA("A name for people."), "description": strA("What it is."),
	"platform": strA("The platform that serves it."), "platform_type": strA("The platform's type."), "realm": strA("The realm."),
	"jurisdiction": strA("The jurisdiction."), "platform_jurisdiction": strA("The platform's jurisdiction."),
	"state": strA("ACTIVE, DEPRECATED or RETIRED."), "revision": intA("The revision; an update must name it."),
	"managed_by": strA("The SPIFFE ID of the automation that manages it (the lock); empty when nobody does."),
	"labels":     mapA("Labels."), "visibility": strA("The visibility mode."),
	"definition":    jsonA("What the platform's plugin needs to reach the target."),
	"access_levels": jsonA("The access levels and their grants."),
	"bindings":      intA("How many bindings use it."), "pending_change_request_id": strA("A change request of it that is still pending, if any."),
}

func bindingVals(m adminclient.Binding) (vals, error) {
	params, err := jsonV("parameters", m.Parameters)
	if err != nil {
		return nil, err
	}
	return vals{
		"name": sv(m.Metadata.Name), "source": sv(m.Source), "target": sv(m.Target), "access_level": sv(m.AccessLevel), "purpose": sp(m.Purpose),
		"expires_at": tv(m.ExpiresAt), "platform": sv(m.Metadata.Platform), "platform_type": sv(m.Metadata.PlatformType), "realm": sv(m.Metadata.Realm),
		"source_jurisdiction": sv(m.Metadata.SourceJurisdiction), "target_jurisdiction": sv(m.Metadata.TargetJurisdiction),
		"platform_jurisdiction": sv(m.Metadata.PlatformJurisdiction), "revision": iv(m.Metadata.Revision), "managed_by": sp(m.Metadata.ManagedBy),
		"labels": labelsV(m.Labels), "parameters": params, "phase": sv(string(m.Status.Phase)), "message": sv(m.Status.Message),
		"propagation_pending": bv(m.Status.PropagationPending), "pending_change_request_id": stv(ptrID(m.Status.PendingChangeRequestId)),
	}, nil
}

var bindingItem = map[string]schema.Attribute{
	"name": strA("The name."), "source": strA("The source."), "target": strA("The target."), "access_level": strA("The access level."),
	"purpose": strA("Why it exists."), "expires_at": strA("When it expires (RFC 3339), if it does."), "platform": strA("The platform."),
	"platform_type": strA("The platform's type."), "realm": strA("The realm."), "source_jurisdiction": strA("The source's jurisdiction."),
	"target_jurisdiction": strA("The target's jurisdiction."), "platform_jurisdiction": strA("The platform's jurisdiction."),
	"revision": intA("The revision; an update must name it."), "managed_by": strA("The SPIFFE ID of the automation that manages it (the lock); empty when nobody does."),
	"labels": mapA("Labels."), "parameters": jsonA("The parameters of the access."), "phase": strA("PROVISIONING, ACTIVE, FAILED, DELETING or DELETE_FAILED."),
	"message": strA("The last message of the workflow."), "propagation_pending": boolA("Whether a change is still being propagated."),
	"pending_change_request_id": strA("A change request of it that is still pending, if any."),
}

func platformVals(m adminclient.Platform) (vals, error) {
	v := vals{
		"name": sv(m.Name), "display_name": sp(m.DisplayName), "description": sp(m.Description), "jurisdiction": sv(m.Jurisdiction),
		"realm": sv(m.Realm), "identity_provider": sv(m.IdentityProvider.Instance), "secret_store": sv(m.SecretStore.Instance),
		"allowed_source_trust_domains": strsV(m.AllowedSourceTrustDomains),
	}
	if m.Revision != nil {
		v["revision"] = iv(*m.Revision)
	}
	if m.Quarantined != nil {
		v["quarantined"] = bv(*m.Quarantined)
	}
	if m.TargetJurisdictions != nil {
		v["target_jurisdictions"] = strsV(*m.TargetJurisdictions)
	}
	if m.Health != nil {
		v["serving"] = bv(m.Health.Serving)
	}
	return v, nil
}

var platformItem = map[string]schema.Attribute{
	"name": strA("The name."), "display_name": strA("A name for people."), "description": strA("What it is."), "jurisdiction": strA("The jurisdiction."),
	"realm": strA("The realm."), "identity_provider": strA("The identity provider instance it uses."), "secret_store": strA("The secret store instance it uses."),
	"allowed_source_trust_domains": listA("The trust domains of the sources that may use it."), "target_jurisdictions": listA("The jurisdictions its targets may have."),
	"revision": intA("The revision."), "quarantined": boolA("Whether ligo isolated it after repeated failures."), "serving": boolA("Whether its plugin is healthy."),
}

// instanceVals maps an identity provider or a secret store. Only a person who
// may file or decide on its jurisdiction gets the revision and the definition:
// for anyone else they are null, not zero.
func instanceVals(m adminclient.Instance) (vals, error) {
	v := vals{
		"name": sv(m.Name), "display_name": sp(m.DisplayName), "description": sp(m.Description), "type": sv(m.Type),
		"jurisdiction": sv(m.Jurisdiction), "realm": sv(m.Realm),
	}
	if m.Revision != nil {
		v["revision"] = iv(*m.Revision)
	}
	if m.Quarantined != nil {
		v["quarantined"] = bv(*m.Quarantined)
	}
	var err error
	if v["network_flows"], err = jsonV("network_flows", m.NetworkFlows); err != nil {
		return nil, err
	}
	if m.Contacts != nil {
		if v["contacts"], err = jsonV("contacts", *m.Contacts); err != nil {
			return nil, err
		}
	}
	if m.Definition != nil && m.Definition.Config != nil {
		if v["config"], err = jsonV("config", *m.Definition.Config); err != nil {
			return nil, err
		}
	}
	return v, nil
}

var instanceItem = map[string]schema.Attribute{
	"name": strA("The name."), "display_name": strA("A name for people."), "description": strA("What it is."), "type": strA("The plugin type and version, such as openldap.v1."),
	"jurisdiction": strA("The jurisdiction."), "realm": strA("The realm."),
	"revision": intA("The revision; null for someone who may not file on its jurisdiction, and for an instance that server.yaml defines."), "quarantined": boolA("Whether its stored row does not verify: it is not in force."),
	"network_flows": jsonA("The network flows it needs."), "contacts": jsonA("Who to ask."),
	"config": jsonSecretA("The plugin's configuration; null for someone who may not file on its jurisdiction. It names a credential, it never holds one."),
}

func instanceEntity(kind, plural, desc string, get func(*adminclient.Client, context.Context, string) (adminclient.Instance, error),
	list func(*adminclient.Client, context.Context) ([]adminclient.Instance, error)) *entity {
	return &entity{
		kind: kind, plural: plural, desc: desc, item: instanceItem,
		get: func(ctx context.Context, c *adminclient.Client, name string) (vals, error) {
			i, err := get(c, ctx, name)
			if err != nil {
				return nil, err
			}
			return instanceVals(i)
		},
		list: func(ctx context.Context, c *adminclient.Client, _ filters) iter.Seq2[vals, error] {
			return func(yield func(vals, error) bool) {
				is, err := list(c, ctx)
				if err != nil {
					yield(nil, err)
					return
				}
				for _, i := range is {
					v, err := instanceVals(i)
					if !yield(v, err) {
						return
					}
				}
			}
		},
	}
}

func catalogVals(m adminclient.CatalogTarget) (vals, error) {
	v := vals{
		"name": sv(m.Name), "display_name": sp(m.DisplayName), "description": sp(m.Description), "jurisdiction": sv(m.Jurisdiction),
		"platform": sv(m.Platform), "platform_type": sv(m.PlatformType), "platform_jurisdiction": sv(m.PlatformJurisdiction),
		"state": sv(string(m.State)), "labels": labelsV(m.Labels),
	}
	if m.Bound != nil {
		v["bound"] = bv(*m.Bound)
	}
	return v, nil
}

var catalogItem = map[string]schema.Attribute{
	"name": strA("The name."), "display_name": strA("A name for people."), "description": strA("What it is."), "jurisdiction": strA("The jurisdiction."),
	"platform": strA("The platform."), "platform_type": strA("The platform's type."), "platform_jurisdiction": strA("The platform's jurisdiction."),
	"state": strA("ACTIVE or DEPRECATED."), "labels": mapA("Labels."), "bound": boolA("With for_source: whether that source is already bound to it."),
}

func catalogCardVals(m adminclient.CatalogDetail) (vals, error) {
	out, err := jsonV("outcomes", m.Outcomes)
	if err != nil {
		return nil, err
	}
	lv, err := jsonV("access_levels", m.AccessLevels)
	if err != nil {
		return nil, err
	}
	return vals{
		"name": sv(m.Metadata.Name), "display_name": sp(m.DisplayName), "description": sp(m.Description), "jurisdiction": sv(m.Metadata.Jurisdiction),
		"platform": sv(m.Metadata.Platform), "platform_type": sv(m.Metadata.PlatformType), "platform_jurisdiction": sv(m.Metadata.PlatformJurisdiction),
		"state": sv(string(m.Metadata.State)), "labels": labelsV(m.Labels), "access_levels": lv, "outcomes": out,
	}, nil
}

var catalogCardItem = map[string]schema.Attribute{
	"name": strA("The name."), "display_name": strA("A name for people."), "description": strA("What it is."), "jurisdiction": strA("The jurisdiction."),
	"platform": strA("The platform."), "platform_type": strA("The platform's type."), "platform_jurisdiction": strA("The platform's jurisdiction."),
	"state": strA("ACTIVE or DEPRECATED."), "labels": mapA("Labels."), "access_levels": jsonA("The access levels."),
	"outcomes": jsonA("For each of the caller's sources, what the charter says: allowed or not, by which rule, and which sides must consent."),
}

func holdVals(m adminclient.Hold) (vals, error) {
	return vals{
		"id": sv(m.Id.String()), "binding": sv(m.BindingName), "source": sv(m.Source), "target": sv(m.Target), "access_level": sv(m.AccessLevel),
		"managed_by": sv(m.ManagedBy), "basis": sv(string(m.Basis)), "deleted_by": sv(m.DeletedBy), "deleted_at": tv(&m.DeletedAt),
		"change_request_id": sv(m.ChangeRequestId.String()), "lifted_by": sp(m.LiftedBy), "lifted_at": tv(m.LiftedAt), "lift_comment": sp(m.LiftComment),
	}, nil
}

var holdItem = map[string]schema.Attribute{
	"id": strA("The hold's id, for ligo hold lift."), "binding": strA("The binding that a person removed in an emergency."), "source": strA("Its source."),
	"target": strA("Its target."), "access_level": strA("Its access level."), "managed_by": strA("The automation that owned the binding."),
	"basis": strA("CONTAINMENT or BREAK_GLASS."), "deleted_by": strA("Who removed it."), "deleted_at": strA("When (RFC 3339)."),
	"change_request_id": strA("The change request of the removal."), "lifted_by": strA("Who lifted the hold, if it was."), "lifted_at": strA("When."),
	"lift_comment": strA("Why."),
}

func charterVals(c adminclient.Charter) (vals, error) {
	p, err := jsonV("policy", c.Policy)
	if err != nil {
		return nil, err
	}
	return vals{"policy": p, "sha256": sv(c.Sha256)}, nil
}

var charterItem = map[string]schema.Attribute{
	"policy": jsonA("The charter in force."), "sha256": strA("The SHA-256 of the charter in force."),
}

// entities returns what the provider reads.
func entities() []*entity {
	srcF, tgtF := commonFilters(), commonFilters()
	srcF["trust_domain"] = filterAttr{desc: "Only the sources of this trust domain."}
	tgtF["platform"], tgtF["platform_type"] = filterAttr{desc: "Only the targets of this platform."}, filterAttr{desc: "Only the targets of platforms of this type."}
	return []*entity{
		{
			kind: "source", plural: "sources", desc: "A source: a set of workloads, named by a SPIFFE ID regex.", item: sourceItem, filters: srcF,
			get: func(ctx context.Context, c *adminclient.Client, name string) (vals, error) {
				d, err := c.GetSource(ctx, name)
				if err != nil {
					return nil, err
				}
				return sourceVals(adminclient.Source{ApiVersion: d.ApiVersion, Identity: d.Identity, Labels: d.Labels, Metadata: d.Metadata, Status: d.Status,
					Visibility: d.Visibility, DisplayName: d.DisplayName, Description: d.Description})
			},
			list: func(ctx context.Context, c *adminclient.Client, f filters) iter.Seq2[vals, error] {
				return pages(c.ListSources(ctx, adminclient.SourceFilter{Jurisdiction: f.s["jurisdiction"], TrustDomain: f.s["trust_domain"], State: f.s["state"],
					Label: f.s["label"], Q: f.s["q"], PageSize: 200}), sourceVals)
			},
		},
		{
			kind: "target", plural: "targets", desc: "A target: a resource that workloads reach through bindings, on a platform.", item: targetItem, filters: tgtF,
			get: func(ctx context.Context, c *adminclient.Client, name string) (vals, error) {
				d, err := c.GetTarget(ctx, name)
				if err != nil {
					return nil, err
				}
				return targetVals(adminclient.Target{AccessLevels: d.AccessLevels, ApiVersion: d.ApiVersion, Definition: d.Definition, Description: d.Description,
					DisplayName: d.DisplayName, Labels: d.Labels, Metadata: d.Metadata, Status: d.Status, Visibility: d.Visibility})
			},
			list: func(ctx context.Context, c *adminclient.Client, f filters) iter.Seq2[vals, error] {
				return pages(c.ListTargets(ctx, adminclient.TargetFilter{Q: f.s["q"], Platform: f.s["platform"], PlatformType: f.s["platform_type"],
					Jurisdiction: f.s["jurisdiction"], Label: f.s["label"], State: f.s["state"], PageSize: 200}), targetVals)
			},
		},
		{
			kind: "binding", plural: "bindings", desc: "A binding: the access that a source has to a target.", item: bindingItem,
			filters: map[string]filterAttr{
				"source": {desc: "Only the bindings of this source."}, "target": {desc: "Only the bindings of this target."},
				"access_level": {desc: "Only this access level."}, "platform": {desc: "Only this platform."}, "platform_type": {desc: "Only platforms of this type."},
				"phase": {desc: "Only this phase."}, "trust_domain": {desc: "Only sources of this trust domain."}, "realm": {desc: "Only this realm."},
				"jurisdiction": {desc: "Only bindings with this jurisdiction pattern in any of their three jurisdictions."},
			},
			get: func(ctx context.Context, c *adminclient.Client, name string) (vals, error) {
				d, err := c.GetBinding(ctx, name)
				if err != nil {
					return nil, err
				}
				return bindingVals(adminclient.Binding{AccessLevel: d.AccessLevel, ExpiresAt: d.ExpiresAt, Labels: d.Labels, Metadata: d.Metadata,
					Parameters: d.Parameters, Purpose: d.Purpose, Source: d.Source, Status: d.Status, Target: d.Target})
			},
			list: func(ctx context.Context, c *adminclient.Client, f filters) iter.Seq2[vals, error] {
				return pages(c.ListBindings(ctx, adminclient.BindingFilter{Source: f.s["source"], Target: f.s["target"], AccessLevel: f.s["access_level"],
					Platform: f.s["platform"], PlatformType: f.s["platform_type"], Phase: f.s["phase"], TrustDomain: f.s["trust_domain"],
					Realm: f.s["realm"], Jurisdiction: f.s["jurisdiction"], PageSize: 200}), bindingVals)
			},
		},
		{
			kind: "platform", plural: "platforms", desc: "A platform: an instance of a platform plugin, with its identity provider and secret store.", item: platformItem,
			get: func(ctx context.Context, c *adminclient.Client, name string) (vals, error) {
				p, err := c.GetPlatform(ctx, name)
				if err != nil {
					return nil, err
				}
				return platformVals(p)
			},
			list: func(ctx context.Context, c *adminclient.Client, _ filters) iter.Seq2[vals, error] {
				return func(yield func(vals, error) bool) {
					ps, err := c.ListPlatforms(ctx)
					if err != nil {
						yield(nil, err)
						return
					}
					for _, p := range ps {
						v, err := platformVals(p)
						if !yield(v, err) {
							return
						}
					}
				}
			},
		},
		instanceEntity("identity_provider", "identity_providers", "An identity provider instance: how a platform's principals are named and authenticated.",
			(*adminclient.Client).GetIdentityProvider, (*adminclient.Client).ListIdentityProviders),
		instanceEntity("secret_store", "secret_stores", "A secret store instance: where a platform keeps the credentials it hands out.",
			(*adminclient.Client).GetSecretStore, (*adminclient.Client).ListSecretStores),
		{
			kind: "catalog_target", desc: "A target card of the catalog, with what the charter says about each of the caller's sources.", item: catalogCardItem,
			get: func(ctx context.Context, c *adminclient.Client, name string) (vals, error) {
				d, err := c.GetCatalogTarget(ctx, name)
				if err != nil {
					return nil, err
				}
				return catalogCardVals(d)
			},
		},
		{
			kind: "catalog_target", plural: "catalog_targets", desc: "The targets that the caller's sources could request.", item: catalogItem,
			filters: map[string]filterAttr{
				"q": {desc: "Matched in names, display names and descriptions."}, "platform": {desc: "Only this platform."}, "platform_type": {desc: "Only platforms of this type."},
				"jurisdiction": {desc: "Only this jurisdiction pattern."}, "label": {desc: "A label, key=value."},
				"for_source":         {desc: "Only the targets that the charter allows for this source; the ones bound to it are marked."},
				"include_deprecated": {desc: "Include the deprecated targets.", boolean: true},
			},
			list: func(ctx context.Context, c *adminclient.Client, f filters) iter.Seq2[vals, error] {
				return pages(c.SearchCatalog(ctx, adminclient.CatalogFilter{Q: f.s["q"], Platform: f.s["platform"], PlatformType: f.s["platform_type"],
					Jurisdiction: f.s["jurisdiction"], Label: f.s["label"], ForSource: f.s["for_source"], IncludeDeprecated: f.b["include_deprecated"], PageSize: 200}), catalogVals)
			},
		},
		{
			kind: "charter", desc: "The charter in force: the rules that every jurisdiction complies with.", keyless: true, item: charterItem,
			get: func(ctx context.Context, c *adminclient.Client, _ string) (vals, error) {
				ch, err := c.GetCharter(ctx)
				if err != nil {
					return nil, err
				}
				return charterVals(ch)
			},
		},
		{
			kind: "hold", plural: "holds", desc: "The holds that stop a pipeline from creating a binding again: a person removed it in an emergency.", item: holdItem,
			filters: map[string]filterAttr{"binding": {desc: "Only the holds of this binding."}, "all": {desc: "Include the holds that were lifted.", boolean: true}},
			list: func(ctx context.Context, c *adminclient.Client, f filters) iter.Seq2[vals, error] {
				return pages(c.ListHolds(ctx, adminclient.HoldFilter{Binding: f.s["binding"], All: f.b["all"], PageSize: 200}), holdVals)
			},
		},
	}
}

// dataSources are the constructors the provider returns.
func dataSources() []func() datasource.DataSource {
	var out []func() datasource.DataSource
	for _, e := range entities() {
		if e.get != nil {
			out = append(out, func() datasource.DataSource { return &singular{e: e} })
		}
		if e.list != nil {
			out = append(out, func() datasource.DataSource { return &plural{e: e} })
		}
	}
	return out
}

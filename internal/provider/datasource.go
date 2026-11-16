package provider

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/dihedron/ligo/pkg/adminclient"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	defaultLimit = 1000
	maxLimit     = 10000
	// readTimeout bounds a plural read: a hostile server that answers with
	// endless empty pages ends here (T-P8).
	readTimeout = 5 * time.Minute
)

// vals are the attributes of one item, by name.
type vals = map[string]attr.Value

// filterAttr is a filter of a plural data source.
type filterAttr struct {
	desc    string
	boolean bool
}

// filters are the filters that the configuration set.
type filters struct {
	s map[string]string
	b map[string]bool
}

// entity describes what a read exposes: a singular data source by name, a
// plural one with filters, or both (spec §3.2). The mapping from the admin
// client's types to attributes is in entities.go.
type entity struct {
	kind    string // ligo_<kind>
	plural  string // ligo_<plural>; empty: no plural form
	desc    string
	keyless bool // a singular form with no name (the charter)
	item    map[string]schema.Attribute
	filters map[string]filterAttr
	get     func(ctx context.Context, c *adminclient.Client, name string) (vals, error) // nil: no singular form
	list    func(ctx context.Context, c *adminclient.Client, f filters) iter.Seq2[vals, error]
}

// session is what Configure handed over; nil before the provider is configured.
func session(data any, diags *diag.Diagnostics) *Session {
	if data == nil {
		return nil
	}
	s, ok := data.(*Session)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *Session, got %T", data))
		return nil
	}
	return s
}

// readError turns an error of the admin client into a diagnostic: the
// problem's status, type and detail, never a body (T-P2).
func readError(diags *diag.Diagnostics, what string, err error) {
	switch {
	case errors.Is(err, adminclient.ErrNotFound):
		diags.AddError("Not found", what+" does not exist, or the caller may not see it: "+err.Error())
	default:
		diags.AddError("Cannot read "+what, err.Error())
	}
}

// complete fills what the mapping did not set with a null of the attribute's type.
func complete(item map[string]schema.Attribute, v vals) vals {
	out := make(vals, len(item))
	for name, a := range item {
		if x, ok := v[name]; ok {
			out[name] = x
			continue
		}
		out[name] = nullOf(a.GetType())
	}
	return out
}

func nullOf(t attr.Type) attr.Value {
	switch t {
	case types.StringType:
		return types.StringNull()
	case types.Int64Type:
		return types.Int64Null()
	case types.BoolType:
		return types.BoolNull()
	}
	if m, ok := t.(types.MapType); ok {
		return types.MapNull(m.ElemType)
	}
	if l, ok := t.(types.ListType); ok {
		return types.ListNull(l.ElemType)
	}
	return types.StringNull()
}

// collect gathers up to limit items. More than limit is an error, not a
// truncation: a plan built on a part of the catalog would be wrong without
// anyone noticing (spec §3.2).
func collect(seq iter.Seq2[vals, error], limit int) ([]vals, error) {
	var out []vals
	for v, err := range seq {
		if err != nil {
			return nil, err
		}
		if len(out) == limit {
			return nil, fmt.Errorf("more than %d items: raise limit (at most %d) or narrow the filters", limit, maxLimit)
		}
		out = append(out, v)
	}
	return out, nil
}

// --- the singular data source ----------------------------------------------

type singular struct {
	e *entity
	s *Session
}

var (
	_ datasource.DataSourceWithConfigure = (*singular)(nil)
	_ datasource.DataSourceWithConfigure = (*plural)(nil)
)

func (d *singular) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "ligo_" + d.e.kind
}

func (d *singular) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{}
	for k, a := range d.e.item {
		attrs[k] = a
	}
	if !d.e.keyless {
		attrs["name"] = schema.StringAttribute{Required: true, Description: "The name of the " + d.e.kind + "."}
	}
	resp.Schema = schema.Schema{Description: d.e.desc, Attributes: attrs}
}

func (d *singular) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.s = session(req.ProviderData, &resp.Diagnostics)
}

func (d *singular) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.s == nil {
		resp.Diagnostics.AddError("The provider is not configured", "Configure the provider before reading "+d.e.kind+".")
		return
	}
	var name string
	if !d.e.keyless {
		if resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &name)...); resp.Diagnostics.HasError() {
			return
		}
	}
	resp.State.Raw = req.Config.Raw
	v, err := d.e.get(ctx, d.s.Client, name)
	if err != nil {
		readError(&resp.Diagnostics, "ligo_"+d.e.kind+" "+name, err)
		return
	}
	for k, x := range complete(d.e.item, v) {
		if resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(k), x)...); resp.Diagnostics.HasError() {
			return
		}
	}
	if !d.e.keyless {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	}
}

// --- the plural data source ------------------------------------------------

type plural struct {
	e *entity
	s *Session
}

func (d *plural) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "ligo_" + d.e.plural
}

func (d *plural) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"limit": schema.Int64Attribute{Optional: true, Description: fmt.Sprintf("At most this many items (default %d, at most %d). More is an error, not a truncation: narrow the filters or raise the limit.", defaultLimit, maxLimit)},
		"items": schema.ListNestedAttribute{Computed: true, Description: "What was found.", NestedObject: schema.NestedAttributeObject{Attributes: d.e.item}},
	}
	for k, f := range d.e.filters {
		if f.boolean {
			attrs[k] = schema.BoolAttribute{Optional: true, Description: f.desc}
		} else {
			attrs[k] = schema.StringAttribute{Optional: true, Description: f.desc}
		}
	}
	resp.Schema = schema.Schema{Description: d.e.desc, Attributes: attrs}
}

func (d *plural) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.s = session(req.ProviderData, &resp.Diagnostics)
}

func (d *plural) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.s == nil {
		resp.Diagnostics.AddError("The provider is not configured", "Configure the provider before reading "+d.e.plural+".")
		return
	}
	f := filters{s: map[string]string{}, b: map[string]bool{}}
	for k, a := range d.e.filters {
		if a.boolean {
			var v types.Bool
			if resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(k), &v)...); resp.Diagnostics.HasError() {
				return
			}
			f.b[k] = v.ValueBool()
			continue
		}
		var v types.String
		if resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(k), &v)...); resp.Diagnostics.HasError() {
			return
		}
		f.s[k] = v.ValueString()
	}
	limit := defaultLimit
	var l types.Int64
	if resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("limit"), &l)...); resp.Diagnostics.HasError() {
		return
	}
	if !l.IsNull() {
		if l.ValueInt64() < 1 || l.ValueInt64() > maxLimit {
			resp.Diagnostics.AddAttributeError(path.Root("limit"), "Invalid limit", fmt.Sprintf("limit is 1 to %d.", maxLimit))
			return
		}
		limit = int(l.ValueInt64())
	}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	items, err := collect(d.e.list(ctx, d.s.Client, f), limit)
	if err != nil {
		readError(&resp.Diagnostics, "ligo_"+d.e.plural, err)
		return
	}
	objType := types.ObjectType{AttrTypes: map[string]attr.Type{}}
	for k, a := range d.e.item {
		objType.AttrTypes[k] = a.GetType()
	}
	elems := make([]attr.Value, 0, len(items))
	for _, v := range items {
		o, diags := types.ObjectValue(objType.AttrTypes, complete(d.e.item, v))
		if resp.Diagnostics.Append(diags...); resp.Diagnostics.HasError() {
			return
		}
		elems = append(elems, o)
	}
	list, diags := types.ListValue(objType, elems)
	if resp.Diagnostics.Append(diags...); resp.Diagnostics.HasError() {
		return
	}
	resp.State.Raw = req.Config.Raw
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("items"), list)...)
}

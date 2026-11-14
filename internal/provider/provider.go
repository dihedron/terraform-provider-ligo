// Package provider is the Terraform and OpenTofu provider of ligo. It has
// no resources yet: they follow the provider's spec, in .specs/.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Ligo is the provider.
type Ligo struct {
	version string
}

var _ provider.Provider = (*Ligo)(nil)

// New returns the provider factory the plugin framework serves.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &Ligo{version: version} }
}

// Metadata names the provider: its resources are ligo_*.
func (p *Ligo) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "ligo"
	resp.Version = p.version
}

// Schema is empty until the spec settles the connection and authentication.
func (p *Ligo) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{}
}

// Configure does nothing yet.
func (p *Ligo) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {}

// Resources is empty.
func (p *Ligo) Resources(context.Context) []func() resource.Resource { return nil }

// DataSources is empty.
func (p *Ligo) DataSources(context.Context) []func() datasource.DataSource { return nil }

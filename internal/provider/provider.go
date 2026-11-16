// Package provider is the Terraform and OpenTofu provider of ligo, built on
// the admin client of ligo's module (pkg/adminclient). Its design is
// .specs/ligo-terraform-provider.md.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
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

// model is the provider's configuration (spec §3.1). No attribute holds a
// password: the password is in the file password_file names.
type model struct {
	ServerURL         types.String `tfsdk:"server_url"`
	ServerID          types.String `tfsdk:"server_id"`
	CACert            types.String `tfsdk:"ca_cert"`
	SpiffeSocket      types.String `tfsdk:"spiffe_socket"`
	Username          types.String `tfsdk:"username"`
	PasswordFile      types.String `tfsdk:"password_file"`
	ApprovalTimeout   types.String `tfsdk:"approval_timeout"`
	ApprovalReference types.String `tfsdk:"approval_reference"`
	AutomationContext types.Map    `tfsdk:"automation_context"`
	Managed           types.Bool   `tfsdk:"managed"`
}

// Schema is the provider's configuration, spec §3.1.
func (p *Ligo) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The provider of ligo, the SPIFFE binding lifecycle service. It files change requests through ligo's admin API and waits for them; it never approves one.",
		Attributes: map[string]schema.Attribute{
			"server_url": schema.StringAttribute{Optional: true, Description: "The admin API, an https:// URL. Environment: LIGO_SERVER_URL."},
			"server_id": schema.StringAttribute{Optional: true, Description: "The SPIFFE ID ligo must present when it is verified against the bundle of the SPIFFE Workload API. " +
				"Default: spiffe://<the caller's trust domain>/ns/sys/sa/ligo."},
			"ca_cert":       schema.StringAttribute{Optional: true, Description: "A path to a PEM bundle that verifies the server, instead of the Workload API's bundle (or, with credentials, the system's roots)."},
			"spiffe_socket": schema.StringAttribute{Optional: true, Description: "The SPIFFE Workload API socket; the provider presents the caller's SVID over mTLS. Environment: SPIFFE_ENDPOINT_SOCKET."},
			"username":      schema.StringAttribute{Optional: true, Description: "An LDAP user, with password_file, when there is no Workload API. A person is never trusted automation: every write waits for an approver."},
			"password_file": schema.StringAttribute{Optional: true, Description: "A file that holds the LDAP password and that nobody but its owner may read. There is no attribute that holds the password."},
			"approval_timeout": schema.StringAttribute{Optional: true, Description: "How long a write waits for its change request, a duration such as 10m (the default); at most 24h. " +
				"A request that is still pending then is an error; it stays in ligo and may still be approved."},
			"approval_reference": schema.StringAttribute{Optional: true, Description: "The upstream change record (a ticket, a pipeline run) sent with every filing; ligo requires it for a delegated approval."},
			"managed": schema.BoolAttribute{Optional: true, Description: "The default of every source, target and binding: true (the default) files them as managed by this automation, so that only it changes them (the lock). " +
				"Each resource's own managed overrides it. A run with username is a person and cannot own: set it to false."},
			"automation_context": schema.MapAttribute{Optional: true, ElementType: types.StringType, Description: "Who runs the automation (job, workspace, run URL), recorded by ligo for traceability; never used for authorization."},
		},
	}
}

// Configure builds the session that every resource and data source uses.
func (p *Ligo) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var m model
	if resp.Diagnostics.Append(req.Config.Get(ctx, &m)...); resp.Diagnostics.HasError() {
		return
	}
	s, err := connect(ctx, m)
	if err != nil {
		resp.Diagnostics.AddError("Cannot configure the ligo provider", err.Error())
		return
	}
	tflog.Debug(ctx, "ligo provider configured", map[string]any{"server": s.Server})
	resp.DataSourceData, resp.ResourceData = s, s
}

// Resources are what the provider writes: the sources so far (chunk 4a).
func (p *Ligo) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{newSourceResource}
}

// DataSources are the reads of chunk 2 (the instances come with chunk 3).
func (p *Ligo) DataSources(context.Context) []func() datasource.DataSource { return dataSources() }

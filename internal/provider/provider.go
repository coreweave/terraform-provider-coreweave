package provider

import (
	"context"
	"fmt"
	"os"

	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/cks"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/networking"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// TestProtoV6ProviderFactories are used to instantiate a provider during
// acceptance testing. The factory function will be invoked for every Terraform
// CLI command executed to create a provider server to which the CLI can
// reattach.
var TestProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"coreweave": providerserver.NewProtocol6WithError(
		func() provider.Provider {
			return &CoreweaveProvider{
				version: "test",
			}
		}(),
	),
}

// Ensure CoreweaveProvider satisfies various provider interfaces.
var _ provider.Provider = &CoreweaveProvider{}
var _ provider.ProviderWithFunctions = &CoreweaveProvider{}

// CoreweaveProvider defines the provider implementation.
type CoreweaveProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// CoreweaveProviderModel describes the provider data model.
type CoreweaveProviderModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Token    types.String `tfsdk:"token"`
}

func (p *CoreweaveProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "coreweave"
	resp.Version = p.version
}

func (p *CoreweaveProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "CoreWeave API Endpoint",
				Optional:            true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "CoreWeave API Token",
				Optional:            true,
				Sensitive:           true,
			},
		},
	}
}

func (p *CoreweaveProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data CoreweaveProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// env vars take precendence
	token := os.Getenv("COREWEAVE_API_TOKEN")
	endpoint := os.Getenv("COREWEAVE_API_ENDPOINT")

	if token == "" && data.Token.IsNull() {
		resp.Diagnostics.AddError("token is required for provider instantiation", "")
		return
	}

	if endpoint == "" {
		if data.Endpoint.IsNull() {
			endpoint = "https://api.coreweave.com/"
		} else {
			endpoint = data.Endpoint.ValueString()
		}
	}

	interceptor := connect.UnaryInterceptorFunc(
		func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Add("Authorization", fmt.Sprintf("Bearer CW-SECRET-%s", token))
				return next(ctx, req)
			}
		},
	)

	client := coreweave.NewClient(endpoint, interceptor)
	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *CoreweaveProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		cks.NewClusterResource,
		networking.NewVpcResource,
	}
}

func (p *CoreweaveProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func (p *CoreweaveProvider) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &CoreweaveProvider{
			version: version,
		}
	}
}

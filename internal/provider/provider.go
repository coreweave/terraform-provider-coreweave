package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	CoreweaveApiTokenEnvVar     string        = "COREWEAVE_API_TOKEN"
	CoreweaveApiEndpointEnvVar  string        = "COREWEAVE_API_ENDPOINT"
	CoreweaveHTTPTimeoutEnvVar  string        = "COREWEAVE_HTTP_TIMEOUT"
	CoreweaveApiEndpointDefault string        = "https://api.coreweave.com/"
	DefaultHTTPTimeout          time.Duration = 10 * time.Second
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
var (
	_ provider.Provider              = &CoreweaveProvider{}
	_ provider.ProviderWithFunctions = &CoreweaveProvider{}
)

// CoreweaveProvider defines the provider implementation.
type CoreweaveProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// CoreweaveProviderModel describes the provider data model.
type CoreweaveProviderModel struct {
	Endpoint    types.String `tfsdk:"endpoint"`
	Token       types.String `tfsdk:"token"`
	HTTPTimeout types.String `tfsdk:"http_timeout"`
}

func (p *CoreweaveProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "coreweave"
	resp.Version = p.version
}

func (p *CoreweaveProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("CoreWeave API Endpoint. This can also be set via the %s environment variable, which takes precedence. Defaults to https://api.coreweave.com/", CoreweaveApiEndpointEnvVar),
				Optional:            true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("CoreWeave API Token. In the form CW-SECRET-<secret>. This can also be set via the %s environment variable, which takes precedence.", CoreweaveApiTokenEnvVar),
				Optional:            true,
				Sensitive:           true,
			},
			"http_timeout": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("Timeout duration for the HTTP client to use. This can also be set via the %s environment variable, which takes precedence. If unset, defaults to 10 seconds", CoreweaveHTTPTimeoutEnvVar),
				Optional:            true,
				Validators: []validator.String{
					durationValidator{},
				},
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

	client, err := BuildClient(ctx, data, req.TerraformVersion, p.version)
	if err != nil {
		resp.Diagnostics.AddError("failed to create coreweave client", err.Error())
		return
	}
	resp.DataSourceData = client
	resp.ResourceData = client
}

func parseDuration(raw string) (*time.Duration, error) {
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		// Try appending “s” to treat it as seconds
		if parsed, err2 := time.ParseDuration(raw + "s"); err2 == nil {
			return &parsed, nil
		}

		return nil, err
	}

	return &parsed, nil
}

// Builds a CW client using the provided model, including any defaults or environment variables.
// Returns an error if the token is not provided.
// Variable precedence: 1) env, 2) config, 3) default/error.
func BuildClient(ctx context.Context, model CoreweaveProviderModel, tfVersion, providerVersion string) (*coreweave.Client, error) {
	endpoint := model.Endpoint.ValueString()
	token := model.Token.ValueString()
	httpTimeout := model.HTTPTimeout.ValueString()
	timeout := DefaultHTTPTimeout

	// An error should not be able to happen in this case, as we specify a validator on the StringAttribute on the provider schema
	// but for posterity we check for the error anyway
	if userSpecified, err := parseDuration(httpTimeout); err == nil {
		timeout = *userSpecified
	}

	if tokenFromEnv, ok := os.LookupEnv(CoreweaveApiTokenEnvVar); ok {
		token = tokenFromEnv
	}
	if endpointFromEnv, ok := os.LookupEnv(CoreweaveApiEndpointEnvVar); ok {
		endpoint = endpointFromEnv
	}
	if timeoutStr, ok := os.LookupEnv(CoreweaveHTTPTimeoutEnvVar); ok {
		timeoutOverride, err := parseDuration(timeoutStr)
		if err == nil {
			timeout = *timeoutOverride
		} else {
			tflog.Error(ctx, fmt.Sprintf("got invalid duration '%s' for %s, using default timeout %v", timeoutStr, CoreweaveHTTPTimeoutEnvVar, DefaultHTTPTimeout))
		}
	}

	if token == "" {
		return nil, errors.New("token is required for coreweave client instantiation")
	}
	if endpoint == "" {
		endpoint = CoreweaveApiEndpointDefault
	}

	tflog.Debug(ctx, fmt.Sprintf("using http client timeout: %v", timeout))

	headerInterceptor := connect.UnaryInterceptorFunc(
		func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Add("Authorization", fmt.Sprintf("Bearer %s", token))
				req.Header().Add("User-Agent", fmt.Sprintf("Terraform/%s terraform-provider-coreweave/%s (+https://github.com/coreweave/terraform-provider-coreweave)", tfVersion, providerVersion))
				return next(ctx, req)
			}
		},
	)

	return coreweave.NewClient(endpoint, timeout, headerInterceptor), nil
}

func (p *CoreweaveProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		cks.NewClusterResource,
		networking.NewVpcResource,
	}
}

func (p *CoreweaveProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		networking.NewVpcDataSource,
		cks.NewClusterDataSource,
	}
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

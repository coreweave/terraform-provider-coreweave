package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	calleridentity "github.com/coreweave/terraform-provider-coreweave/coreweave/caller_identity"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/cks"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/inference"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/networking"
	objectstorage "github.com/coreweave/terraform-provider-coreweave/coreweave/object_storage"
	workloadfederation "github.com/coreweave/terraform-provider-coreweave/coreweave/workload_federation"
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
	CoreweaveApiTokenEnvVar     string        = "COREWEAVE_API_TOKEN"    //nolint:gosec,staticcheck
	CoreweaveApiEndpointEnvVar  string        = "COREWEAVE_API_ENDPOINT" //nolint:gosec,staticcheck
	CoreWeaveS3EndpointEnvVar   string        = "COREWEAVE_S3_ENDPOINT"
	CoreweaveHTTPTimeoutEnvVar  string        = "COREWEAVE_HTTP_TIMEOUT"
	CoreweaveApiEndpointDefault string        = "https://api.coreweave.com/" //nolint:staticcheck
	CoreWeaveS3EndpointDefault  string        = "https://cwobject.com"
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
	S3Endpoint  types.String `tfsdk:"s3_endpoint"`
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
				MarkdownDescription: fmt.Sprintf("CoreWeave API Endpoint. This can also be set via the %s environment variable, which takes precedence. Defaults to `%s`", CoreweaveApiEndpointEnvVar, CoreweaveApiEndpointDefault),
				Optional:            true,
				Validators: []validator.String{
					uriValidator{},
				},
			},
			"s3_endpoint": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("CoreWeave S3 Endpoint, used for CoreWeave Object Storage. This can also be set via the %s environment variable, which takes precedence. Defaults to `%s`", CoreWeaveS3EndpointEnvVar, CoreWeaveS3EndpointDefault),
				Optional:            true,
				Validators: []validator.String{
					uriValidator{},
				},
			},
			"token": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("CoreWeave API Token in the form `CW-SECRET-<secret>`. This can also be set via the %s environment variable, which takes precedence.", CoreweaveApiTokenEnvVar),
				Optional:            true,
				Sensitive:           true,
			},
			"http_timeout": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("Timeout for each CoreWeave API and Object Storage S3 HTTP attempt. This can also be set via the %s environment variable, which takes precedence. When unset, CoreWeave API attempts default to 10 seconds and S3 attempts default to 30 seconds. Values near or below service latency can cause request timeouts.", CoreweaveHTTPTimeoutEnvVar),
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
			if parsed <= 0 {
				return nil, fmt.Errorf("duration must be greater than zero")
			}
			return &parsed, nil
		}

		return nil, err
	}
	if parsed <= 0 {
		return nil, fmt.Errorf("duration must be greater than zero")
	}

	return &parsed, nil
}

func resolveHTTPTimeouts(ctx context.Context, configured types.String) (time.Duration, time.Duration) {
	connectTimeout := DefaultHTTPTimeout
	s3Timeout := coreweave.DefaultS3AttemptTimeout

	if !configured.IsNull() && !configured.IsUnknown() && configured.ValueString() != "" {
		if parsed, err := parseDuration(configured.ValueString()); err == nil {
			connectTimeout = *parsed
			s3Timeout = *parsed
		}
	}

	if timeoutStr, ok := os.LookupEnv(CoreweaveHTTPTimeoutEnvVar); ok {
		timeoutOverride, err := parseDuration(timeoutStr)
		if err == nil {
			connectTimeout = *timeoutOverride
			s3Timeout = *timeoutOverride
		} else {
			tflog.Error(ctx, fmt.Sprintf("got invalid duration '%s' for %s, using configured/default timeouts", timeoutStr, CoreweaveHTTPTimeoutEnvVar))
		}
	}

	return connectTimeout, s3Timeout
}

// Builds a CW client using the provided model, including any defaults or environment variables.
// Returns an error if the token is not provided.
// Variable precedence: 1) env, 2) config, 3) default/error.
//
//nolint:staticcheck
func BuildClient(ctx context.Context, model CoreweaveProviderModel, tfVersion, providerVersion string) (*coreweave.Client, error) {
	endpoint := model.Endpoint.ValueString()
	s3Endpoint := model.S3Endpoint.ValueString()
	token := model.Token.ValueString()
	timeout, s3Timeout := resolveHTTPTimeouts(ctx, model.HTTPTimeout)

	if tokenFromEnv, ok := os.LookupEnv(CoreweaveApiTokenEnvVar); ok {
		token = tokenFromEnv
	}
	if endpointFromEnv, ok := os.LookupEnv(CoreweaveApiEndpointEnvVar); ok {
		endpoint = endpointFromEnv
	}
	if s3EndpointFrmEnv, ok := os.LookupEnv(CoreWeaveS3EndpointEnvVar); ok {
		s3Endpoint = s3EndpointFrmEnv
	}
	if token == "" {
		return nil, errors.New("token is required for coreweave client instantiation")
	}
	if endpoint == "" {
		endpoint = CoreweaveApiEndpointDefault
	}
	if s3Endpoint == "" {
		s3Endpoint = CoreWeaveS3EndpointDefault
	}

	tflog.Debug(ctx, fmt.Sprintf("using http client timeout: %v", timeout))
	userAgent := fmt.Sprintf("Terraform/%s terraform-provider-coreweave/%s (+https://github.com/coreweave/terraform-provider-coreweave)", tfVersion, providerVersion)

	headerInterceptor := connect.UnaryInterceptorFunc(
		func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Add("Authorization", fmt.Sprintf("Bearer %s", token))
				req.Header().Set("User-Agent", userAgent)
				return next(ctx, req)
			}
		},
	)

	return coreweave.NewClientWithOptions(
		endpoint,
		s3Endpoint,
		timeout,
		token,
		userAgent,
		coreweave.ClientOptions{S3AttemptTimeout: s3Timeout},
		headerInterceptor,
		coreweave.TFLogInterceptor(),
	), nil
}

func (p *CoreweaveProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		cks.NewClusterResource,
		networking.NewVpcResource,
		objectstorage.NewBucketResource,
		objectstorage.NewOrganizationAccessPolicyResource,
		objectstorage.NewBucketLifecycleResource,
		objectstorage.NewBucketInventoryResource,
		objectstorage.NewBucketVersioningResource,
		objectstorage.NewBucketPolicyResource,
		objectstorage.NewBucketSettingsResource,
		inference.NewInferenceDeploymentResource,
		inference.NewInferenceCapacityClaimResource,
		inference.NewInferenceGatewayResource,
		workloadfederation.NewOIDCConfigResource,
	}
}

func (p *CoreweaveProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		calleridentity.NewCallerIdentityDataSource,
		networking.NewVpcDataSource,
		cks.NewClusterDataSource,
		objectstorage.NewBucketPolicyDocumentDataSource,
		inference.NewInferenceDeploymentParametersDataSource,
		inference.NewCapacityClaimParametersDataSource,
		inference.NewGatewayParametersDataSource,
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

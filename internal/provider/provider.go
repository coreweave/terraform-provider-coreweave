package provider

import (
	"context"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	calleridentity "github.com/coreweave/terraform-provider-coreweave/coreweave/caller_identity"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/cks"
	containerregistry "github.com/coreweave/terraform-provider-coreweave/coreweave/container_registry"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/inference"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/networking"
	objectstorage "github.com/coreweave/terraform-provider-coreweave/coreweave/object_storage"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/sandbox"
	serviceaccount "github.com/coreweave/terraform-provider-coreweave/coreweave/service_account"
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
	_ provider.Provider                     = &CoreweaveProvider{}
	_ provider.ProviderWithConfigValidators = &CoreweaveProvider{}
	_ provider.ProviderWithFunctions        = &CoreweaveProvider{}
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
	OrgID          types.String                 `tfsdk:"org_id"`
	Endpoint       types.String                 `tfsdk:"endpoint"`
	S3Endpoint     types.String                 `tfsdk:"s3_endpoint"`
	Token          types.String                 `tfsdk:"token"`
	HTTPTimeout    types.String                 `tfsdk:"http_timeout"`
	Authentication *AuthenticationProviderModel `tfsdk:"authentication"`
}

func (p *CoreweaveProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "coreweave"
	resp.Version = p.version
}

func (p *CoreweaveProvider) ConfigValidators(context.Context) []provider.ConfigValidator {
	return []provider.ConfigValidator{conflictingAuthenticationValidator{}}
}

func (p *CoreweaveProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"org_id": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("The target organization for resource operations. This is separate from the organization owning an authenticating service account and can also be set with `%s`.", CoreweaveOrgIDEnvVar),
				Optional:            true,
			},
			"authentication": authenticationSchema(),
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
				MarkdownDescription: fmt.Sprintf("CoreWeave API Token in the form `CW-SECRET-<secret>`. This can also be set via the %s environment variable, which takes precedence when service-account authentication is not explicitly configured.", CoreweaveApiTokenEnvVar),
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
	resp.Diagnostics.Append(checkAuthenticationKnown(ctx, req.Config)...)
	if resp.Diagnostics.HasError() {
		return
	}

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

// BuildClient constructs a CoreWeave client using static-token or
// service-account authentication. Existing endpoint, S3 endpoint, timeout, and
// static-token environment precedence is preserved. Explicit service-account
// authentication takes precedence over an ambient static token.
//
//nolint:staticcheck
func BuildClient(ctx context.Context, model CoreweaveProviderModel, tfVersion, providerVersion string) (*coreweave.Client, error) {
	endpoint := model.Endpoint.ValueString()
	s3Endpoint := model.S3Endpoint.ValueString()
	timeout, s3Timeout := resolveHTTPTimeouts(ctx, model.HTTPTimeout)

	if endpointFromEnv, ok := os.LookupEnv(CoreweaveApiEndpointEnvVar); ok {
		endpoint = endpointFromEnv
	}
	if s3EndpointFrmEnv, ok := os.LookupEnv(CoreWeaveS3EndpointEnvVar); ok {
		s3Endpoint = s3EndpointFrmEnv
	}
	if endpoint == "" {
		endpoint = CoreweaveApiEndpointDefault
	}
	if s3Endpoint == "" {
		s3Endpoint = CoreWeaveS3EndpointDefault
	}

	tflog.Debug(ctx, fmt.Sprintf("using http client timeout: %v", timeout))
	userAgent := fmt.Sprintf("Terraform/%s terraform-provider-coreweave/%s (+https://github.com/coreweave/terraform-provider-coreweave)", tfVersion, providerVersion)

	tokenSource, err := resolveAccessTokenSource(model, endpoint, userAgent, timeout)
	if err != nil {
		return nil, err
	}
	userAgentInterceptor := func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			info, _ := connect.CallInfoForClientContext(ctx)
			info.RequestHeader().Set("User-Agent", userAgent)
			return next(ctx, spec)
		}
	}

	return coreweave.NewClientWithOptions(
		endpoint,
		s3Endpoint,
		timeout,
		tokenSource,
		userAgent,
		coreweave.ClientOptions{
			S3AttemptTimeout:       s3Timeout,
			ResourceOrganizationID: resolveResourceOrganizationID(model),
		},
		userAgentInterceptor,
		coreweave.TFLogInterceptor(),
	)
}

func (p *CoreweaveProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		cks.NewClusterResource,
		sandbox.NewManagedRunnerResource,
		networking.NewVpcResource,
		objectstorage.NewBucketResource,
		objectstorage.NewAccessKeyResource,
		objectstorage.NewOrganizationAccessPolicyResource,
		objectstorage.NewBucketLifecycleResource,
		objectstorage.NewBucketInventoryResource,
		objectstorage.NewBucketVersioningResource,
		objectstorage.NewBucketPolicyResource,
		objectstorage.NewBucketSettingsResource,
		inference.NewInferenceDeploymentResource,
		inference.NewInferenceHotLoadResource,
		inference.NewInferenceCapacityClaimResource,
		inference.NewInferenceGatewayResource,
		workloadfederation.NewOIDCConfigResource,
		serviceaccount.NewOIDCTrustConfigResource,
		serviceaccount.NewServiceAccountResource,
		containerregistry.NewNamespaceResource,
		containerregistry.NewAccessConfigurationResource,
		containerregistry.NewLifecyclePolicyResource,
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
		workloadfederation.NewOIDCConfigDataSource,
		containerregistry.NewNamespaceDataSource,
		containerregistry.NewNamespacesDataSource,
		containerregistry.NewZonesDataSource,
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

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
	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
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
	Endpoint       types.String                 `tfsdk:"endpoint"`
	S3Endpoint     types.String                 `tfsdk:"s3_endpoint"`
	Token          types.String                 `tfsdk:"token"`
	HTTPTimeout    types.String                 `tfsdk:"http_timeout"`
	Authentication *AuthenticationProviderModel `tfsdk:"authentication"`
}

type AuthenticationProviderModel struct {
	WorkloadIdentity *WorkloadIdentityProviderModel `tfsdk:"workload_identity"`
}

type WorkloadIdentityProviderModel struct {
	ServiceAccountUID types.String `tfsdk:"service_account_uid"`
}

func (p *CoreweaveProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "coreweave"
	resp.Version = p.version
}

func (p *CoreweaveProvider) ConfigValidators(context.Context) []provider.ConfigValidator {
	return []provider.ConfigValidator{
		conflictingAuthenticationValidator{},
	}
}

// conflictingAuthenticationValidator rejects configuring both a static token
// and workload identity.
//
// providervalidator.Conflicting is not used because it treats token = "" as
// configured. That is a credential nobody supplied -- it is what token = var.x
// yields when x is left at its empty default -- and reporting it as a conflict
// with workload identity sends the practitioner looking for a token they never
// set. BuildClient applies the same rule so both layers agree.
type conflictingAuthenticationValidator struct{}

func (conflictingAuthenticationValidator) Description(ctx context.Context) string {
	return conflictingAuthenticationValidator{}.MarkdownDescription(ctx)
}

func (conflictingAuthenticationValidator) MarkdownDescription(context.Context) string {
	return "Ensures that `token` and `authentication.workload_identity` are not both configured."
}

func (conflictingAuthenticationValidator) ValidateProvider(ctx context.Context, req provider.ValidateConfigRequest, resp *provider.ValidateConfigResponse) {
	tokenPath := path.Root("token")
	var token types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, tokenPath, &token)...)

	workloadIdentityPath := path.Root("authentication").AtName("workload_identity")
	var workloadIdentity types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, workloadIdentityPath, &workloadIdentity)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !staticTokenConfigured(token) || workloadIdentity.IsNull() || workloadIdentity.IsUnknown() {
		return
	}

	resp.Diagnostics.AddAttributeError(
		tokenPath,
		"Invalid Attribute Combination",
		fmt.Sprintf("%q cannot be configured together with %q.", tokenPath, workloadIdentityPath),
	)
}

// staticTokenConfigured reports whether token supplies a usable credential.
// Unknown values are not yet configured, and an empty string is no credential
// at all.
func staticTokenConfigured(token types.String) bool {
	return !token.IsNull() && !token.IsUnknown() && token.ValueString() != ""
}

func (p *CoreweaveProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"authentication": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Authentication settings. Configure workload identity to exchange HCP Terraform's OIDC token for short-lived CoreWeave credentials.",
				Attributes: map[string]schema.Attribute{
					"workload_identity": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: fmt.Sprintf("Authenticate as a CoreWeave service account using HCP Terraform workload identity. The external OIDC token is read from `%s`.", auth.TerraformCloudWorkloadIdentityTokenEnvVar),
						Attributes: map[string]schema.Attribute{
							"service_account_uid": schema.StringAttribute{
								Required:            true,
								MarkdownDescription: "UID of the CoreWeave service account to authenticate as.",
							},
						},
					},
				},
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("CoreWeave API Endpoint. Must be an absolute `http` or `https` URL, including the scheme. This can also be set via the %s environment variable, which takes precedence. Defaults to `%s`", CoreweaveApiEndpointEnvVar, CoreweaveApiEndpointDefault),
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
				MarkdownDescription: fmt.Sprintf("CoreWeave API Token in the form `CW-SECRET-<secret>`. When workload identity is not configured, this can also be set via the %s environment variable, which takes precedence. An explicitly configured token cannot be used with `authentication.workload_identity`.", CoreweaveApiTokenEnvVar),
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
	// The authentication attributes are modeled as Go structs, which cannot
	// hold an unknown value. Reject unknowns before Config.Get so that an
	// unresolved object produces this diagnostic rather than the framework's
	// "this is always an error in the provider" conversion error.
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

// unknownAuthenticationDetail names the attribute that must be known and
// explains the constraint the practitioner has to satisfy.
func unknownAuthenticationDetail(attribute string) string {
	return fmt.Sprintf(
		"%s must be known while configuring the provider, so it cannot depend on a resource created by the same Terraform configuration. Move the value into a variable or a separate configuration that is applied first.",
		attribute,
	)
}

// checkAuthenticationKnown reports any unknown value in the authentication
// block, from the block itself down to the individual attributes.
func checkAuthenticationKnown(ctx context.Context, config tfsdk.Config) diag.Diagnostics {
	var diagnostics diag.Diagnostics

	// The client is built during Configure, so an unknown token cannot be used
	// and would otherwise reach BuildClient looking like an empty one.
	tokenPath := path.Root("token")
	var token types.String
	diagnostics.Append(config.GetAttribute(ctx, tokenPath, &token)...)
	if diagnostics.HasError() {
		return diagnostics
	}
	if token.IsUnknown() {
		diagnostics.AddAttributeError(tokenPath, "Unknown API token", unknownAuthenticationDetail("token"))
		return diagnostics
	}

	authenticationPath := path.Root("authentication")
	var authentication types.Object
	diagnostics.Append(config.GetAttribute(ctx, authenticationPath, &authentication)...)
	if diagnostics.HasError() {
		return diagnostics
	}
	if authentication.IsUnknown() {
		diagnostics.AddAttributeError(authenticationPath, "Unknown authentication configuration", unknownAuthenticationDetail("authentication"))
		return diagnostics
	}
	if authentication.IsNull() {
		return diagnostics
	}

	workloadIdentityPath := authenticationPath.AtName("workload_identity")
	var workloadIdentity types.Object
	diagnostics.Append(config.GetAttribute(ctx, workloadIdentityPath, &workloadIdentity)...)
	if diagnostics.HasError() {
		return diagnostics
	}
	if workloadIdentity.IsUnknown() {
		diagnostics.AddAttributeError(workloadIdentityPath, "Unknown workload identity configuration", unknownAuthenticationDetail("authentication.workload_identity"))
		return diagnostics
	}
	if workloadIdentity.IsNull() {
		return diagnostics
	}

	serviceAccountUIDPath := workloadIdentityPath.AtName("service_account_uid")
	var serviceAccountUID types.String
	diagnostics.Append(config.GetAttribute(ctx, serviceAccountUIDPath, &serviceAccountUID)...)
	if diagnostics.HasError() {
		return diagnostics
	}
	if serviceAccountUID.IsUnknown() {
		diagnostics.AddAttributeError(
			serviceAccountUIDPath,
			"Unknown workload identity service account UID",
			unknownAuthenticationDetail("authentication.workload_identity.service_account_uid"),
		)
	}

	return diagnostics
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
// Returns an error if neither static-token nor workload-identity authentication is configured.
// Variable precedence: 1) env, 2) config, 3) default/error.
//
//nolint:staticcheck
func BuildClient(ctx context.Context, model CoreweaveProviderModel, tfVersion, providerVersion string) (*coreweave.Client, error) {
	endpoint := model.Endpoint.ValueString()
	s3Endpoint := model.S3Endpoint.ValueString()
	token := model.Token.ValueString()
	timeout, s3Timeout := resolveHTTPTimeouts(ctx, model.HTTPTimeout)

	workloadIdentity := model.Authentication != nil && model.Authentication.WorkloadIdentity != nil
	// Match providervalidator.Conflicting, which skips unknown values: an
	// unknown token is not yet a configured one. Configure rejects unknown
	// tokens up front, so BuildClient only ever sees null or known here.
	explicitStaticToken := staticTokenConfigured(model.Token)
	if workloadIdentity && explicitStaticToken {
		return nil, errors.New("authentication.workload_identity conflicts with the configured token attribute")
	}
	if !workloadIdentity {
		if tokenFromEnv, ok := os.LookupEnv(CoreweaveApiTokenEnvVar); ok {
			token = tokenFromEnv
		}
	}
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

	var tokenSource coreweave.AccessTokenSource
	if workloadIdentity {
		if model.Authentication.WorkloadIdentity.ServiceAccountUID.IsUnknown() {
			return nil, errors.New("authentication.workload_identity.service_account_uid is unknown and must be known while configuring the provider")
		}
		var err error
		// One source per configured client. The client is shared by every
		// resource using this provider configuration, and the source's own
		// cache serves them, so the credential never outlives the
		// configuration that owns it.
		tokenSource, err = auth.NewWorkloadIdentityTokenSource(
			ctx,
			endpoint,
			model.Authentication.WorkloadIdentity.ServiceAccountUID.ValueString(),
			userAgent,
			timeout,
		)
		if err != nil {
			return nil, err
		}
	} else {
		if token == "" {
			return nil, errors.New("token is required when authentication.workload_identity is not configured")
		}
		tokenSource = auth.NewStaticTokenSource(token)
	}
	userAgentInterceptor := connect.UnaryInterceptorFunc(
		func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Set("User-Agent", userAgent)
				return next(ctx, req)
			}
		},
	)

	return coreweave.NewClientWithOptions(
		endpoint,
		s3Endpoint,
		timeout,
		tokenSource,
		userAgent,
		coreweave.ClientOptions{S3AttemptTimeout: s3Timeout},
		userAgentInterceptor,
		coreweave.TFLogInterceptor(),
	)
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
		workloadfederation.NewOIDCConfigDataSource,
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

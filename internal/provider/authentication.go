package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	CoreweaveOrgIDEnvVar                   = "COREWEAVE_ORG_ID"
	CoreweaveServiceAccountIDEnvVar        = "COREWEAVE_SERVICE_ACCOUNT_ID"
	CoreweaveServiceAccountOrgIDEnvVar     = "COREWEAVE_SERVICE_ACCOUNT_ORG_ID"
	CoreweaveServiceAccountTokenFileEnvVar = "COREWEAVE_SERVICE_ACCOUNT_TOKEN_FILE"
)

type AuthenticationProviderModel struct {
	ServiceAccountUID *ServiceAccountAuthenticationProviderModel `tfsdk:"service_account_uid"`
}

type ServiceAccountAuthenticationProviderModel struct {
	ID        types.String `tfsdk:"id"`
	OrgID     types.String `tfsdk:"org_id"`
	TokenFile types.String `tfsdk:"token_file"`
}

func authenticationSchema() schema.Attribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Authentication settings. Configure `service_account_uid` to exchange an external OIDC assertion for short-lived CoreWeave credentials.",
		Attributes: map[string]schema.Attribute{
			"service_account_uid": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Authenticate as a CoreWeave service account. Explicit configuration selects this mode even when `COREWEAVE_API_TOKEN` is present.",
				Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: fmt.Sprintf("The service-account UID. Defaults to `%s` when omitted.", CoreweaveServiceAccountIDEnvVar),
					},
					"org_id": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: fmt.Sprintf("The organization that owns the authenticating service account. Defaults to `%s` when omitted and is independent from the provider's target `org_id`.", CoreweaveServiceAccountOrgIDEnvVar),
					},
					"token_file": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: fmt.Sprintf("Path to a file containing the external OIDC assertion. Defaults to `%s`; when neither is set, `%s` is used.", CoreweaveServiceAccountTokenFileEnvVar, auth.TerraformCloudWorkloadIdentityTokenEnvVar),
					},
				},
			},
		},
	}
}

type conflictingAuthenticationValidator struct{}

func (conflictingAuthenticationValidator) Description(ctx context.Context) string {
	return conflictingAuthenticationValidator{}.MarkdownDescription(ctx)
}

func (conflictingAuthenticationValidator) MarkdownDescription(context.Context) string {
	return "Ensures that `token` and `authentication.service_account_uid` are not both configured."
}

func (conflictingAuthenticationValidator) ValidateProvider(ctx context.Context, req provider.ValidateConfigRequest, resp *provider.ValidateConfigResponse) {
	tokenPath := path.Root("token")
	var token types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, tokenPath, &token)...)

	serviceAccountPath := path.Root("authentication").AtName("service_account_uid")
	var serviceAccount types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, serviceAccountPath, &serviceAccount)...)
	if resp.Diagnostics.HasError() || !staticTokenConfigured(token) || serviceAccount.IsNull() || serviceAccount.IsUnknown() {
		return
	}

	resp.Diagnostics.AddAttributeError(
		tokenPath,
		"Invalid Attribute Combination",
		fmt.Sprintf("%q cannot be configured together with %q.", tokenPath, serviceAccountPath),
	)
}

func staticTokenConfigured(token types.String) bool {
	return !token.IsNull() && !token.IsUnknown() && strings.TrimSpace(token.ValueString()) != ""
}

func checkAuthenticationKnown(ctx context.Context, config tfsdk.Config) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	checks := []struct {
		path  path.Path
		title string
	}{
		{path.Root("org_id"), "Unknown organization ID"},
		{path.Root("token"), "Unknown API token"},
	}
	for _, check := range checks {
		var value types.String
		diagnostics.Append(config.GetAttribute(ctx, check.path, &value)...)
		if diagnostics.HasError() {
			return diagnostics
		}
		if value.IsUnknown() {
			diagnostics.AddAttributeError(check.path, check.title, unknownProviderValueDetail(check.path.String()))
			return diagnostics
		}
	}

	authenticationPath := path.Root("authentication")
	var authentication types.Object
	diagnostics.Append(config.GetAttribute(ctx, authenticationPath, &authentication)...)
	if diagnostics.HasError() {
		return diagnostics
	}
	if authentication.IsUnknown() {
		diagnostics.AddAttributeError(authenticationPath, "Unknown authentication configuration", unknownProviderValueDetail(authenticationPath.String()))
		return diagnostics
	}
	if authentication.IsNull() {
		return diagnostics
	}

	serviceAccountPath := authenticationPath.AtName("service_account_uid")
	var serviceAccount types.Object
	diagnostics.Append(config.GetAttribute(ctx, serviceAccountPath, &serviceAccount)...)
	if diagnostics.HasError() {
		return diagnostics
	}
	if serviceAccount.IsUnknown() {
		diagnostics.AddAttributeError(serviceAccountPath, "Unknown service-account authentication configuration", unknownProviderValueDetail(serviceAccountPath.String()))
		return diagnostics
	}
	if serviceAccount.IsNull() {
		return diagnostics
	}

	for _, name := range []string{"id", "org_id", "token_file"} {
		attributePath := serviceAccountPath.AtName(name)
		var value types.String
		diagnostics.Append(config.GetAttribute(ctx, attributePath, &value)...)
		if diagnostics.HasError() {
			return diagnostics
		}
		if value.IsUnknown() {
			diagnostics.AddAttributeError(attributePath, "Unknown service-account authentication value", unknownProviderValueDetail(attributePath.String()))
			return diagnostics
		}
	}

	return diagnostics
}

func unknownProviderValueDetail(attribute string) string {
	return fmt.Sprintf("%s must be known while configuring the provider, so it cannot depend on a resource created by the same Terraform configuration.", attribute)
}

func resolveResourceOrganizationID(model CoreweaveProviderModel) string {
	if value := knownString(model.OrgID); value != "" {
		return value
	}
	return environmentString(CoreweaveOrgIDEnvVar)
}

func resolveAccessTokenSource(
	model CoreweaveProviderModel,
	endpoint string,
	userAgent string,
	httpTimeout time.Duration,
) (coreweave.AccessTokenSource, error) {
	explicitServiceAccount := model.Authentication != nil && model.Authentication.ServiceAccountUID != nil
	if explicitServiceAccount && staticTokenConfigured(model.Token) {
		return nil, fmt.Errorf("authentication.service_account_uid conflicts with the configured token attribute")
	}

	if explicitServiceAccount {
		return newServiceAccountTokenSource(*model.Authentication.ServiceAccountUID, endpoint, userAgent, httpTimeout)
	}

	token := knownString(model.Token)
	if environmentToken, ok := os.LookupEnv(CoreweaveApiTokenEnvVar); ok {
		token = strings.TrimSpace(environmentToken)
	}
	if token != "" {
		return auth.NewStaticTokenSource(token), nil
	}

	if serviceAccountEnvironmentConfigured() {
		return newServiceAccountTokenSource(ServiceAccountAuthenticationProviderModel{}, endpoint, userAgent, httpTimeout)
	}

	return nil, fmt.Errorf("token is required when service-account authentication is not configured")
}

func newServiceAccountTokenSource(
	model ServiceAccountAuthenticationProviderModel,
	endpoint string,
	userAgent string,
	httpTimeout time.Duration,
) (coreweave.AccessTokenSource, error) {
	serviceAccountID := explicitOrEnvironment(knownString(model.ID), CoreweaveServiceAccountIDEnvVar)
	serviceAccountOrgID := explicitOrEnvironment(knownString(model.OrgID), CoreweaveServiceAccountOrgIDEnvVar)
	tokenFile := explicitOrEnvironment(knownString(model.TokenFile), CoreweaveServiceAccountTokenFileEnvVar)

	var problems []string
	if serviceAccountID == "" {
		problems = append(problems, fmt.Sprintf("authentication.service_account_uid.id or %s is required", CoreweaveServiceAccountIDEnvVar))
	}
	if serviceAccountOrgID == "" {
		problems = append(problems, fmt.Sprintf("authentication.service_account_uid.org_id or %s is required", CoreweaveServiceAccountOrgIDEnvVar))
	}

	var tokenSource auth.OIDCTokenSource
	if tokenFile == "" {
		if environmentString(auth.TerraformCloudWorkloadIdentityTokenEnvVar) == "" {
			problems = append(problems, fmt.Sprintf("authentication.service_account_uid.token_file, %s, or %s is required", CoreweaveServiceAccountTokenFileEnvVar, auth.TerraformCloudWorkloadIdentityTokenEnvVar))
		} else {
			tokenSource = auth.NewEnvironmentOIDCTokenSource(auth.TerraformCloudWorkloadIdentityTokenEnvVar)
		}
	}
	if len(problems) != 0 {
		return nil, fmt.Errorf("invalid service-account authentication configuration: %s", strings.Join(problems, "; "))
	}

	exchangeTimeout := httpTimeout * 3 / 4
	return auth.NewWorkloadIdentityTokenSource(auth.WorkloadIdentityConfig{
		Endpoint:            endpoint,
		ServiceAccountID:    serviceAccountID,
		ServiceAccountOrgID: serviceAccountOrgID,
		TokenFile:           tokenFile,
		TokenSource:         tokenSource,
		ExchangeTimeout:     exchangeTimeout,
		UserAgent:           userAgent,
	})
}

func serviceAccountEnvironmentConfigured() bool {
	for _, name := range []string{
		CoreweaveServiceAccountIDEnvVar,
		CoreweaveServiceAccountOrgIDEnvVar,
		CoreweaveServiceAccountTokenFileEnvVar,
		auth.TerraformCloudWorkloadIdentityTokenEnvVar,
	} {
		if environmentString(name) != "" {
			return true
		}
	}
	return false
}

func explicitOrEnvironment(explicit, environmentVariable string) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	return environmentString(environmentVariable)
}

func environmentString(name string) string {
	value, _ := os.LookupEnv(name)
	return strings.TrimSpace(value)
}

func knownString(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return strings.TrimSpace(value.ValueString())
}

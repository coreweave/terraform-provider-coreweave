package observability

import (
	"context"
	"fmt"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"buf.build/go/protovalidate"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var (
	_ resource.ResourceWithConfigure        = &HTTPSForwardingEndpointResource{}
	_ resource.ResourceWithValidateConfig   = &HTTPSForwardingEndpointResource{}
	_ resource.ResourceWithConfigValidators = &HTTPSForwardingEndpointResource{}
	_ resource.ResourceWithImportState      = &HTTPSForwardingEndpointResource{}
)

func NewForwardingEndpointHTTPSResource() resource.Resource {
	return &HTTPSForwardingEndpointResource{}
}

type HTTPSForwardingEndpointResource struct {
	coretf.CoreResource
}

func (r *HTTPSForwardingEndpointResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_observability_telemetry_relay_endpoint_https"
}

func (r *HTTPSForwardingEndpointResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := commonEndpointSchema()

	attributes["endpoint"] = schema.StringAttribute{
		MarkdownDescription: "The HTTPS endpoint URL. Plain HTTP is rejected, as are targets resolving to loopback, private, or link-local addresses.",
		Required:            true,
		Validators: []validator.String{
			stringvalidator.LengthBetween(1, endpointURLMaxLength),
			stringvalidator.RegexMatches(httpsURLPattern, "must be an https:// URL"),
		},
	}

	// Optional, never Optional+Computed. The proto's three-value enum exists so a
	// client can tell "never configured" (UNSPECIFIED) from "explicitly
	// uncompressed" (NONE); Terraform already draws that line as null vs known.
	// Optional+Computed would make config-null mean "keep prior value", so
	// removing the attribute could never clear it.
	attributes["compression"] = schema.StringAttribute{
		MarkdownDescription: fmt.Sprintf(
			"Compression applied to the request body. Must be one of: %s. Omit for uncompressed delivery; `COMPRESSION_TYPE_NONE` records that compression was explicitly disabled.",
			coreweave.EnumMarkdownValues(typesv1beta1.HTTPSConfig_CompressionType_name, true),
		),
		Optional: true,
		Validators: []validator.String{
			stringvalidator.OneOf(coreweave.EnumValues(typesv1beta1.HTTPSConfig_CompressionType_name, true)...),
		},
	}

	attributes["tls"] = tlsConfigAttribute()

	attributes["credentials"] = schema.SingleNestedAttribute{
		MarkdownDescription: "Authentication credentials for the HTTPS endpoint. Exactly one of `basic_auth` or `auth_headers` must be set. " +
			"The values are write-only: they are sent to the API and never read back, so changing one only takes effect if you also increment `credentials_version`. " +
			"Removing this block clears the stored credentials.",
		Optional: true,
		Attributes: map[string]schema.Attribute{
			"basic_auth":   basicAuthAttribute(),
			"auth_headers": authHeadersAttribute(),
		},
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telemetry Relay HTTPS forwarding endpoint. Forwards telemetry data to an HTTPS endpoint.",
		Attributes:          attributes,
	}
}

// ConfigValidators rejects two auth methods at plan time with the offending
// attributes named. protovalidate catches the other half — the oneof is
// `required`, so an empty credentials block fails too — but only once the
// whole request is assembled, which gives a worse error location.
func (r *HTTPSForwardingEndpointResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.Conflicting(
			path.MatchRoot("credentials").AtName("basic_auth"),
			path.MatchRoot("credentials").AtName("auth_headers"),
		),
	}
}

// buildCreateRequest assembles the CreateEndpointRequest from a model whose
// credentials came from the config. Create, ValidateConfig and the plan-time
// credential checks all go through it, so what gets validated is what gets
// sent.
func buildHTTPSCreateRequest(data model.ForwardingEndpointHTTPS) (*clusterv1beta1.CreateEndpointRequest, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	endpointMsg, diags := data.ToMsg()
	diagnostics.Append(diags...)

	creds, diags := getHTTPSCredentials(data)
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	createReq := &clusterv1beta1.CreateEndpointRequest{
		Ref:  endpointMsg.GetRef(),
		Spec: endpointMsg.GetSpec(),
	}
	// SetHttps sets the credentials oneof; a nil argument clears it.
	if creds != nil {
		createReq.SetHttps(creds)
	}

	return createReq, diagnostics
}

func (r *HTTPSForwardingEndpointResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data model.ForwardingEndpointHTTPS
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Skip validation if any values are unknown (e.g., during plan when the URL
	// or slug comes from another resource). protovalidate would reject the
	// zero-value placeholder and make the resource unusable inside a module.
	if data.Slug.IsUnknown() || data.DisplayName.IsUnknown() || data.Endpoint.IsUnknown() || data.Compression.IsUnknown() {
		return
	}

	// Validate the CreateEndpointRequest, not the bare ForwardingEndpoint. The
	// endpoint message has no credentials field, so validating it left every
	// HTTPSCredentials rule — the required oneof, the header name and value
	// patterns, the reserved-header list — to surface at apply.
	createReq, diagnostics := buildHTTPSCreateRequest(data)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := protovalidate.Validate(createReq); err != nil {
		resp.Diagnostics.AddError("Validation Error", err.Error())
	}
}

func (r *HTTPSForwardingEndpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
}

func getHTTPSCredentials(data model.ForwardingEndpointHTTPS) (credentials *typesv1beta1.HTTPSCredentials, diagnostics diag.Diagnostics) {
	if data.Credentials == nil {
		return nil, nil
	}

	credentials, diags := data.Credentials.ToMsg()
	diagnostics.Append(diags...)
	return credentials, diagnostics
}

func (r *HTTPSForwardingEndpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Read the config first: write-only attributes are stripped from the plan,
	// so the config is the only place the credential values survive.
	var config model.ForwardingEndpointHTTPS
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq, diags := buildHTTPSCreateRequest(config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var data model.ForwardingEndpointHTTPS
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, diags := createEndpoint(ctx, r.Client, createReq)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save state before polling. The endpoint exists the moment CreateEndpoint
	// returns; if the poll below fails or times out and state was never written,
	// the endpoint is orphaned and the next apply fails AlreadyExists.
	resp.Diagnostics.Append(data.Set(created)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint, diags := awaitEndpointReady(ctx, r.Client, createReq.GetRef())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(data.Set(endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HTTPSForwardingEndpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data model.ForwardingEndpointHTTPS
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := readEndpoint(ctx, r.Client, data.Slug.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// Deleted out of band: drop it from state so the next plan re-creates it,
	// rather than dereferencing a nil endpoint below.
	if endpoint == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	if endpoint.Spec.WhichConfig() != typesv1beta1.ForwardingEndpointSpec_Https_case {
		resp.Diagnostics.AddError("Invalid Endpoint Type", "The endpoint is not an HTTPS endpoint")
		return
	}

	resp.Diagnostics.Append(data.Set(endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HTTPSForwardingEndpointResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Credentials come from the config, not the plan. Write-only attributes are
	// null in the plan, so reading them from there built a credentials message
	// of empty strings, which the API's min_len rule rejected — a display_name
	// change on a credentialed endpoint failed with no way to fix it.
	var config model.ForwardingEndpointHTTPS
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state model.ForwardingEndpointHTTPS
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var data model.ForwardingEndpointHTTPS
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointMsg, diagnostics := data.ToMsg()
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	updateReq := &clusterv1beta1.UpdateEndpointRequest{
		Ref:  endpointMsg.GetRef(),
		Spec: endpointMsg.GetSpec(),
	}

	action := credentialAction(
		config.Credentials != nil,
		state.CredentialsConfigured.ValueBool(),
		state.CredentialsVersion,
		config.CredentialsVersion,
	)
	updateReq.SetCredentialAction(action)

	// Only REPLACE carries a payload. PRESERVE and CLEAR must leave the oneof
	// unset, or the server rejects the request.
	if action == clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_REPLACE {
		creds, diags := getHTTPSCredentials(config)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		updateReq.SetHttps(creds)
	}

	updated, diags := updateEndpoint(ctx, r.Client, updateReq)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Same reasoning as Create: once UpdateEndpoint returns, the server has
	// already applied the change. Record it before polling so a poll failure
	// doesn't leave state describing the pre-update endpoint.
	resp.Diagnostics.Append(data.Set(updated)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint, diags := awaitEndpointReady(ctx, r.Client, updateReq.GetRef())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(data.Set(endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HTTPSForwardingEndpointResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data model.ForwardingEndpointHTTPS
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(deleteEndpoint(ctx, r.Client, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.State.RemoveResource(ctx)
}

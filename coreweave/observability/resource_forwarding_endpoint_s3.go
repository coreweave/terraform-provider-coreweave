package observability

import (
	"context"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"buf.build/go/protovalidate"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var (
	_ resource.ResourceWithConfigure      = &S3ForwardingEndpointResource{}
	_ resource.ResourceWithValidateConfig = &S3ForwardingEndpointResource{}
	_ resource.ResourceWithImportState    = &S3ForwardingEndpointResource{}
)

func NewForwardingEndpointS3Resource() resource.Resource {
	return &S3ForwardingEndpointResource{}
}

type S3ForwardingEndpointResource struct {
	coretf.CoreResource
}

func (r *S3ForwardingEndpointResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_observability_telemetry_relay_endpoint_s3"
}

func (r *S3ForwardingEndpointResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := commonEndpointSchema()

	// Named `uri` after the proto field it maps to. It carries an optional key
	// prefix as well as the bucket, so `bucket` would have been a lie.
	attributes["uri"] = schema.StringAttribute{
		MarkdownDescription: "The S3 URI where data is written, in the form `s3://bucket-name/optional/prefix/`.",
		Required:            true,
		Validators: []validator.String{
			stringvalidator.LengthBetween(1, s3URIMaxLength),
			stringvalidator.RegexMatches(s3URIPattern, "must be an s3:// URI with a lowercase bucket name, e.g. s3://my-bucket/prefix/"),
		},
	}

	attributes["region"] = schema.StringAttribute{
		MarkdownDescription: "The region where the bucket is located. Required for AWS S3; for S3-compatible stores use the store's own region value.",
		Required:            true,
		Validators: []validator.String{
			stringvalidator.LengthBetween(1, s3RegionMaxLength),
		},
	}

	attributes["credentials"] = s3CredentialsAttribute()

	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telemetry Relay S3 forwarding endpoint. Forwards telemetry data to an S3-compatible bucket.",
		Attributes:          attributes,
	}
}

// buildS3CreateRequest assembles the CreateEndpointRequest from a model whose
// credentials came from the config, so ValidateConfig checks the same message
// Create sends — including the request's message-level CEL rule that S3
// endpoints must have credentials.
func buildS3CreateRequest(data model.ForwardingEndpointS3) (*clusterv1beta1.CreateEndpointRequest, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	endpointMsg, diags := data.ToMsg()
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	createReq := &clusterv1beta1.CreateEndpointRequest{
		Ref:  endpointMsg.GetRef(),
		Spec: endpointMsg.GetSpec(),
	}
	if creds := getS3Credentials(data); creds != nil {
		createReq.SetS3(creds)
	}

	return createReq, diagnostics
}

func (r *S3ForwardingEndpointResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data model.ForwardingEndpointS3
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Skip validation if any values are unknown (e.g., during plan when the URI
	// or slug comes from another resource).
	if data.Slug.IsUnknown() || data.DisplayName.IsUnknown() || data.URI.IsUnknown() || data.Region.IsUnknown() {
		return
	}

	// S3 credentials are mandatory at create and can never be cleared — the
	// request carries a message-level CEL rule for each half. Say so here, at
	// the attribute, rather than letting the server's CEL message surface.
	if data.Credentials == nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("credentials"),
			"Missing S3 credentials",
			"S3 forwarding endpoints require credentials. They are mandatory at creation and cannot be removed afterwards; to stop using these credentials, replace the endpoint.",
		)
		return
	}

	createReq, diagnostics := buildS3CreateRequest(data)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := protovalidate.Validate(createReq); err != nil {
		resp.Diagnostics.AddError("Validation Error", err.Error())
	}
}

func (r *S3ForwardingEndpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
}

func getS3Credentials(data model.ForwardingEndpointS3) *typesv1beta1.S3Credentials {
	if data.Credentials == nil {
		return nil
	}

	return data.Credentials.ToMsg()
}

func (r *S3ForwardingEndpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Read the config first: write-only attributes are stripped from the plan,
	// so the config is the only place the credential values survive.
	var config model.ForwardingEndpointS3
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq, diags := buildS3CreateRequest(config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var data model.ForwardingEndpointS3
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, diags := createEndpoint(ctx, r.Client, createReq)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save state before polling so a poll failure cannot orphan the endpoint.
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

func (r *S3ForwardingEndpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data model.ForwardingEndpointS3
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := readEndpoint(ctx, r.Client, data.Slug.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// Deleted out of band: drop it from state so the next plan re-creates it.
	if endpoint == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	if endpoint.Spec.WhichConfig() != typesv1beta1.ForwardingEndpointSpec_S3_case {
		resp.Diagnostics.AddError("Invalid Endpoint Type", "The endpoint is not an S3 endpoint")
		return
	}

	resp.Diagnostics.Append(data.Set(endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *S3ForwardingEndpointResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Credentials come from the config: write-only attributes are null in the
	// plan, so reading them from there sent empty strings the API rejects.
	var config model.ForwardingEndpointS3
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state model.ForwardingEndpointS3
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var data model.ForwardingEndpointS3
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

	// CLEAR is unreachable here: ValidateConfig requires a credentials block on
	// every S3 endpoint, matching the request's CEL rule
	// "credentials cannot be cleared for S3 endpoints".
	if action == clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_REPLACE {
		updateReq.SetS3(getS3Credentials(config))
	}

	updated, diags := updateEndpoint(ctx, r.Client, updateReq)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save state before polling so a poll failure doesn't leave state describing
	// the pre-update endpoint.
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

func (r *S3ForwardingEndpointResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data model.ForwardingEndpointS3
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

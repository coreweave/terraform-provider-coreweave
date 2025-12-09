package telecaster

import (
	"context"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"buf.build/go/protovalidate"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-log/tflog"
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
	resp.TypeName = req.ProviderTypeName + "_telecaster_forwarding_endpoint_s3"
}

func (r *S3ForwardingEndpointResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := commonEndpointSchema()

	attributes["bucket"] = schema.StringAttribute{
		MarkdownDescription: "The S3 bucket URI (e.g., s3://bucket-name).",
		Required:            true,
	}

	attributes["region"] = schema.StringAttribute{
		MarkdownDescription: "The AWS region for the S3 bucket.",
		Required:            true,
	}

	attributes["credentials"] = s3CredentialsAttribute()

	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telecaster S3 forwarding endpoint. Forwards telemetry data to an S3-compatible bucket.",
		Attributes:          attributes,
	}
}

func (r *S3ForwardingEndpointResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data model.ForwardingEndpointS3Model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	msg, diagnostics := data.ToMsg()
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := protovalidate.Validate(msg); err != nil {
		resp.Diagnostics.AddError("Validation Error", err.Error())
	}
}

func (r *S3ForwardingEndpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
}

func getS3Credentials(data model.ForwardingEndpointS3Model) *typesv1beta1.S3Credentials {
	if data.Credentials == nil {
		return nil
	}

	return data.Credentials.ToMsg()
}

func (r *S3ForwardingEndpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data model.ForwardingEndpointS3Model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	// Grab the creds before we load the full plan, because write-only attributes are removed at the plan stage.
	creds := getS3Credentials(data)

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointMsg, diagnostics := data.ToMsg()
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq := &clusterv1beta1.CreateEndpointRequest{
		Ref:  endpointMsg.Ref,
		Spec: endpointMsg.Spec,
	}

	// SetS3 sets the credentials oneof with the S3Credentials.
	createReq.SetS3(creds)

	endpoint, diags := createEndpoint(ctx, r.Client, createReq)
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
	var data model.ForwardingEndpointS3Model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := readEndpoint(ctx, r.Client, data.Slug.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
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

	tflog.Debug(ctx, "Reading Telecaster S3 Forwarding Endpoint", map[string]any{
		"slug":         data.Slug.ValueString(),
		"display_name": data.DisplayName.ValueString(),
		"bucket":       data.Bucket.ValueString(),
		"region":       data.Region.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *S3ForwardingEndpointResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data model.ForwardingEndpointS3Model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	// Grab the creds before we load the full plan, because write-only attributes are removed at the plan stage.
	creds := getS3Credentials(data)

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

	updateReq.SetS3(creds)

	endpoint, diags := updateEndpoint(ctx, r.Client, updateReq)
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
	var data model.ForwardingEndpointS3Model
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

package telecaster

import (
	"context"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"buf.build/go/protovalidate"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	httpsEndpointTimeout = 10 * time.Minute
)

var (
	_ resource.ResourceWithConfigure      = &HTTPSForwardingEndpointResource{}
	_ resource.ResourceWithValidateConfig = &HTTPSForwardingEndpointResource{}
	_ resource.ResourceWithImportState    = &HTTPSForwardingEndpointResource{}
)

func NewForwardingEndpointHTTPSResource() resource.Resource {
	return &HTTPSForwardingEndpointResource{}
}

type HTTPSForwardingEndpointResource struct {
	coretf.CoreResource
}

func (r *HTTPSForwardingEndpointResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telecaster_forwarding_endpoint_https"
}

func (r *HTTPSForwardingEndpointResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := commonEndpointSchema()

	attributes["endpoint"] = schema.StringAttribute{
		MarkdownDescription: "The HTTPS endpoint URL.",
		Required:            true,
	}

	attributes["tls"] = tlsConfigAttribute()

	attributes["credentials"] = schema.SingleNestedAttribute{
		MarkdownDescription: "Authentication credentials for the HTTPS endpoint. At most one of basic_auth, bearer_token, or auth_headers should be set.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"basic_auth": basicAuthAttribute(),
			"bearer_token": bearerTokenAttribute(),
			"auth_headers": authHeadersAttribute(),
		},
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telecaster HTTPS forwarding endpoint. Forwards telemetry data to an HTTPS endpoint.",
		Attributes:          attributes,
	}
}

func (r *HTTPSForwardingEndpointResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data model.ForwardingEndpointHTTPSModel
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

func (r *HTTPSForwardingEndpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
}

func getHTTPSCredentials(data model.ForwardingEndpointHTTPSModel) (credentials *typesv1beta1.HTTPSCredentials, diagnostics diag.Diagnostics) {
	if data.Credentials == nil {
		return nil, nil
	}

	credentials, diags := data.Credentials.ToMsg()
	diagnostics.Append(diags...)
	return credentials, diagnostics
}

func (r *HTTPSForwardingEndpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data model.ForwardingEndpointHTTPSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	// Grab the creds before we load the full plan, because write-only attributes are removed at the plan stage.
	creds, diags := getHTTPSCredentials(data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

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

	// SetHttps sets the credentials oneof with the HTTPSCredentials.
	createReq.SetHttps(creds)

	endpoint := createEndpoint(ctx, r.Client, createReq, httpsEndpointTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Set(endpoint)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HTTPSForwardingEndpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data model.ForwardingEndpointHTTPSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := readEndpoint(ctx, r.Client, data.Slug.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
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

	tflog.Debug(ctx, "Reading Telecaster HTTPS Forwarding Endpoint", map[string]any{
		"slug":         data.Slug.ValueString(),
		"display_name": data.DisplayName.ValueString(),
		"endpoint":     data.Endpoint.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HTTPSForwardingEndpointResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data model.ForwardingEndpointHTTPSModel
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

	creds, diags := getHTTPSCredentials(data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	updateReq.SetHttps(creds)

	endpoint, diags := updateEndpoint(ctx, r.Client, updateReq, httpsEndpointTimeout)
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
	var data model.ForwardingEndpointHTTPSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointMsg, diagnostics := data.ToMsg()
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := internal.DeleteEndpointAndWait(ctx, r.Client, endpointMsg.GetRef()); err != nil {
		resp.Diagnostics.AddError("Error deleting Telecaster endpoint", err.Error())
		return
	}

	resp.State.RemoveResource(ctx)
}

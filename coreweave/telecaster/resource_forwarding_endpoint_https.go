package telecaster

import (
	"context"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

type HTTPSForwardingEndpointModel struct {
	endpointCommon

	Endpoint    types.String                 `tfsdk:"endpoint"`
	TLS         *model.TLSConfigModel        `tfsdk:"tls"`
	Credentials *model.HTTPSCredentialsModel `tfsdk:"credentials"`
}

func (m *HTTPSForwardingEndpointModel) setFromEndpoint(ctx context.Context, endpoint *typesv1beta1.ForwardingEndpoint) (diagnostics diag.Diagnostics) {
	if endpoint == nil {
		return
	}

	diagnostics.Append(m.endpointCommon.setFromEndpoint(endpoint)...)

	if endpoint.Spec != nil && endpoint.Spec.GetHttps() != nil {
		httpsConfig := endpoint.Spec.GetHttps()
		m.Endpoint = types.StringValue(httpsConfig.Endpoint)

		if httpsConfig.Tls != nil {
			m.TLS = new(model.TLSConfigModel)
			m.TLS.Set(httpsConfig.Tls)
		}
	}

	return
}

func (m *HTTPSForwardingEndpointModel) toMsg(ctx context.Context) (msg *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	httpsConfig := &typesv1beta1.HTTPSConfig{
		Endpoint: m.Endpoint.ValueString(),
		Tls:      m.TLS.ToMsg(),
	}

	msg = &typesv1beta1.ForwardingEndpoint{
		Ref: m.toRef(),
		Spec: &typesv1beta1.ForwardingEndpointSpec{
			DisplayName: m.DisplayName.ValueString(),
			Config: &typesv1beta1.ForwardingEndpointSpec_Https{
				Https: httpsConfig,
			},
		},
	}
	msg.Spec.SetHttps(httpsConfig)

	return msg, diagnostics
}

func (m *HTTPSForwardingEndpointModel) toCredentials() (credentials *typesv1beta1.HTTPSCredentials, diagnostics diag.Diagnostics) {
	if m.Credentials == nil {
		return nil, nil
	}

	credentials, diags := m.Credentials.ToMsg()
	diagnostics.Append(diags...)

	return credentials, diagnostics
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
			"basic_auth": schema.SingleNestedAttribute{
				MarkdownDescription: "HTTP Basic authentication credentials.",
				Optional:            true,
				Validators: []validator.Object{
					objectvalidator.ExactlyOneOf(
						path.MatchRoot("credentials").AtName("basic_auth"),
						path.MatchRoot("credentials").AtName("bearer_token"),
						path.MatchRoot("credentials").AtName("auth_headers"),
					),
				},
				Attributes: map[string]schema.Attribute{
					"username": schema.StringAttribute{
						MarkdownDescription: "Username for HTTP Basic authentication.",
						Required:            true,
						Sensitive:           true,
						WriteOnly:           true,
					},
					"password": schema.StringAttribute{
						MarkdownDescription: "Password for HTTP Basic authentication.",
						Required:            true,
						Sensitive:           true,
						WriteOnly:           true,
					},
				},
			},
			"bearer_token": schema.SingleNestedAttribute{
				MarkdownDescription: "Bearer token authentication credentials.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"token": schema.StringAttribute{
						MarkdownDescription: "Bearer token value.",
						Required:            true,
						Sensitive:           true,
						WriteOnly:           true,
					},
				},
			},
			"auth_headers": schema.SingleNestedAttribute{
				MarkdownDescription: "Custom HTTP headers for authentication.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"headers": schema.MapAttribute{
						MarkdownDescription: "Map of HTTP header names to values for authentication.",
						Required:            true,
						Sensitive:           true,
						WriteOnly:           true,
						ElementType:         types.StringType,
					},
				},
			},
		},
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telecaster HTTPS forwarding endpoint. Forwards telemetry data to an HTTPS endpoint.",
		Attributes:          attributes,
	}
}

func (r *HTTPSForwardingEndpointResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data HTTPSForwardingEndpointModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	msg, diags := data.toMsg(ctx)
	resp.Diagnostics.Append(diags...)
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

func (r *HTTPSForwardingEndpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HTTPSForwardingEndpointModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointMsg, diags := data.toMsg(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq := &clusterv1beta1.CreateEndpointRequest{
		Ref:  endpointMsg.Ref,
		Spec: endpointMsg.Spec,
	}

	// Add credentials if provided
	httpsCreds, diags := data.toCredentials()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if httpsCreds != nil {
		createReq.SetHttps(httpsCreds)
	}

	if _, err := r.Client.CreateEndpoint(ctx, connect.NewRequest(createReq)); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	endpoint, err := pollForEndpointReady(ctx, r.Client, endpointMsg.Ref, httpsEndpointTimeout)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(data.setFromEndpoint(ctx, endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HTTPSForwardingEndpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data HTTPSForwardingEndpointModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint, err := readEndpointBySlug(ctx, r.Client, data.Slug.ValueString())
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	if endpoint == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	if configType := endpoint.Spec.WhichConfig(); configType != typesv1beta1.ForwardingEndpointSpec_Https_case {
		resp.Diagnostics.AddError(
			"Invalid Endpoint Type",
			"The endpoint exists but is not an HTTPS endpoint. Actual type: "+configType.String(),
		)
		return
	}

	resp.Diagnostics.Append(data.setFromEndpoint(ctx, endpoint)...)
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
	var data HTTPSForwardingEndpointModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointMsg, diags := data.toMsg(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateReq := &clusterv1beta1.UpdateEndpointRequest{
		Ref:  endpointMsg.Ref,
		Spec: endpointMsg.Spec,
	}

	httpsCreds, diags := data.toCredentials()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if httpsCreds != nil {
		updateReq.SetHttps(httpsCreds)
	}

	if _, err := r.Client.UpdateEndpoint(ctx, connect.NewRequest(updateReq)); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	endpoint, err := pollForEndpointReady(ctx, r.Client, endpointMsg.Ref, httpsEndpointTimeout)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(data.setFromEndpoint(ctx, endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HTTPSForwardingEndpointResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data HTTPSForwardingEndpointModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteEndpoint(ctx, r.Client, data.toRef(), httpsEndpointTimeout, &resp.Diagnostics)
}

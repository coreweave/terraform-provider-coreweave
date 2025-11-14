package telecaster

import (
	"context"
	"fmt"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	telecastertypesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

var (
	_ resource.ResourceWithConfigure   = &ForwardingEndpointResource{}
	_ resource.ResourceWithImportState = &ForwardingEndpointResource{}
)

func NewForwardingEndpointResource() resource.Resource {
	return &ForwardingEndpointResource{}
}

type ForwardingEndpointResource struct {
	coretf.CoreResource
}

type ForwardingEndpointResourceModel struct {
	Ref    types.Object `tfsdk:"ref"`
	Spec   types.Object `tfsdk:"spec"`
	Status types.Object `tfsdk:"status"`
}

func (e *ForwardingEndpointResourceModel) ToMsg(ctx context.Context) (msg *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	if e == nil {
		return nil, nil
	}

	var (
		ref  model.ForwardingEndpointRefModel
		spec model.ForwardingEndpointSpecModel
	)

	diagnostics.Append(e.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(e.Spec.As(ctx, &spec, basetypes.ObjectAsOptions{})...)

	refProto, diags := ref.ToMsg()
	diagnostics.Append(diags...)
	specProto, diags := spec.ToMsg()
	diagnostics.Append(diags...)

	if diagnostics.HasError() {
		return nil, diagnostics
	}

	msg = &typesv1beta1.ForwardingEndpoint{
		Ref:  refProto,
		Spec: specProto,
	}

	return
}

func (e *ForwardingEndpointResourceModel) Set(ctx context.Context, data *telecastertypesv1beta1.ForwardingEndpoint) (diagnostics diag.Diagnostics) {
	var ref model.ForwardingEndpointRefModel
	// calling .As ensures that any nested `types.Object`s are properly initialized.
	diagnostics.Append(e.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(ref.Set(data.Ref)...)
	refObj, diags := types.ObjectValueFrom(ctx, e.Ref.AttributeTypes(ctx), &ref)
	diagnostics.Append(diags...)
	e.Ref = refObj

	var spec model.ForwardingEndpointSpecModel
	diagnostics.Append(e.Spec.As(ctx, &spec, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(spec.Set(data.Spec)...)
	specObj, diags := types.ObjectValueFrom(ctx, e.Spec.AttributeTypes(ctx), &spec)
	diagnostics.Append(diags...)
	e.Spec = specObj

	var status model.ForwardingEndpointStatusModel
	diagnostics.Append(e.Status.As(ctx, &status, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(status.Set(data.Status)...)
	statusObj, diags := types.ObjectValueFrom(ctx, e.Status.AttributeTypes(ctx), &status)
	diagnostics.Append(diags...)
	e.Status = statusObj

	return
}

func (f *ForwardingEndpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ref").AtName("slug"), req, resp)
}

func (f *ForwardingEndpointResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telecaster_forwarding_endpoint"
}

func (f *ForwardingEndpointResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telecaster forwarding endpoint",
		Attributes: map[string]schema.Attribute{
			"ref": schema.SingleNestedAttribute{
				MarkdownDescription: "Identifying information for the forwarding endpoint.",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"slug": schema.StringAttribute{
						MarkdownDescription: "The slug of the forwarding endpoint. Used as a unique identifier.",
						Required:            true,
					},
				},
			},
			"spec": schema.SingleNestedAttribute{
				MarkdownDescription: "The specification for the forwarding endpoint.",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"display_name": schema.StringAttribute{
						MarkdownDescription: "The display name of the forwarding endpoint.",
						Required:            true,
					},
					"kafka": schema.SingleNestedAttribute{
						MarkdownDescription: "Kafka forwarding endpoint configuration.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"bootstrap_endpoints": schema.StringAttribute{
								MarkdownDescription: "The Kafka bootstrap endpoints.",
								Required:            true,
							},
							"topic": schema.StringAttribute{
								MarkdownDescription: "The Kafka topic.",
								Required:            true,
							},
							"tls": tlsConfigModelAttribute(),
							"scram_auth": schema.SingleNestedAttribute{
								MarkdownDescription: "SCRAM authentication configuration for Kafka.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"mechanism": schema.StringAttribute{
										MarkdownDescription: "The SCRAM mechanism (e.g., SCRAM-SHA-256, SCRAM-SHA-512).",
										Optional:            true,
									},
								},
							},
						},
					},
					"prometheus": schema.SingleNestedAttribute{
						MarkdownDescription: "Prometheus forwarding endpoint configuration.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"endpoint": schema.StringAttribute{
								MarkdownDescription: "The Prometheus remote write endpoint.",
								Required:            true,
							},
							"tls": tlsConfigModelAttribute(),
							"basic_auth": schema.SingleNestedAttribute{
								MarkdownDescription: "Basic authentication configuration for Prometheus.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"username": schema.StringAttribute{
										MarkdownDescription: "The username for basic authentication.",
										Required:            true,
										WriteOnly:           true,
									},
									"password": schema.StringAttribute{
										MarkdownDescription: "The password for basic authentication.",
										Required:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
								},
							},
						},
					},
					"s3": schema.SingleNestedAttribute{
						MarkdownDescription: "S3 forwarding endpoint configuration.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"uri": schema.StringAttribute{
								MarkdownDescription: "The S3 URI.",
								Required:            true,
							},
							"region": schema.StringAttribute{
								MarkdownDescription: "The S3 region.",
								Required:            true,
							},
							"requires_credentials": schema.BoolAttribute{
								MarkdownDescription: "Whether the S3 endpoint requires credentials.",
								Optional:            true,
								Computed:            true,
							},
							// "credentials": schema.SingleNestedAttribute{
							// 	MarkdownDescription: "Credentials configuration for S3.",
							// 	Optional:            true,
							// 	Attributes: map[string]schema.Attribute{
							// 		"access_key_id": schema.StringAttribute{
							// 			MarkdownDescription: "The S3 access key ID.",
							// 			Required:            true,
							// 			Sensitive:           true,
							// 			WriteOnly:           true,
							// 		},
							// 		"secret_access_key": schema.StringAttribute{
							// 			MarkdownDescription: "The S3 secret access key.",
							// 			Required:            true,
							// 			Sensitive:           true,
							// 			WriteOnly:           true,
							// 		},
							// 		"session_token": schema.StringAttribute{
							// 			MarkdownDescription: "The S3 session token.",
							// 			Optional:            true,
							// 			Sensitive:           true,
							// 			WriteOnly:           true,
							// 		},
							// 	},
							// },
						},
					},
					"https": schema.SingleNestedAttribute{
						MarkdownDescription: "HTTPS forwarding endpoint configuration.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"endpoint": schema.StringAttribute{
								MarkdownDescription: "The HTTPS endpoint.",
								Required:            true,
							},
							"tls": tlsConfigModelAttribute(),
							"basic_auth": schema.SingleNestedAttribute{
								MarkdownDescription: "Basic authentication configuration for HTTPS.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"username": schema.StringAttribute{
										MarkdownDescription: "The username for basic authentication.",
										Required:            true,
										WriteOnly:           true,
									},
									"password": schema.StringAttribute{
										MarkdownDescription: "The password for basic authentication.",
										Required:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
								},
							},
							"bearer_token": schema.SingleNestedAttribute{
								MarkdownDescription: "Bearer token authentication configuration for HTTPS.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"token": schema.StringAttribute{
										MarkdownDescription: "The bearer token.",
										Required:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
								},
							},
							"auth_headers": schema.SingleNestedAttribute{
								MarkdownDescription: "Authentication headers configuration for HTTPS.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"headers": schema.MapAttribute{
										MarkdownDescription: "A map of header names to values.",
										ElementType:         types.StringType,
										Required:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
								},
							},
						},
					},
				},
			},
			"status": schema.SingleNestedAttribute{
				MarkdownDescription: "The status of the forwarding endpoint.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"created_at": schema.StringAttribute{
						MarkdownDescription: "The creation time of the forwarding endpoint.",
						Computed:            true,
						CustomType:          timetypes.RFC3339Type{},
					},
					"updated_at": schema.StringAttribute{
						MarkdownDescription: "The last update time of the forwarding endpoint.",
						Computed:            true,
						CustomType:          timetypes.RFC3339Type{},
					},
					"state_code": schema.Int32Attribute{
						MarkdownDescription: "The state code of the forwarding endpoint.",
						Computed:            true,
					},
					"state": schema.StringAttribute{
						MarkdownDescription: "The state of the forwarding endpoint.",
						Computed:            true,
					},
					"state_message": schema.StringAttribute{
						MarkdownDescription: "The state message of the forwarding endpoint.",
						Computed:            true,
					},
				},
			},
		},
	}
}

func (f *ForwardingEndpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointMsg, diags := data.ToMsg(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := f.Client.CreateEndpoint(ctx, connect.NewRequest(&clusterv1beta1.CreateEndpointRequest{
		Ref:  endpointMsg.Ref,
		Spec: endpointMsg.Spec,
	})); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	pollConf := retry.StateChangeConf{
		Pending: []string{
			// telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: endpointMsg.Ref,
			}))
			if err != nil {
				return nil, telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			return getResp.Msg.Endpoint, getResp.Msg.Endpoint.Status.State.String(), nil
		},
		Timeout: 10 * time.Minute,
	}

	rawEndpoint, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	endpoint, ok := rawEndpoint.(*telecastertypesv1beta1.ForwardingEndpoint)
	if !ok {
		resp.Diagnostics.AddError(
			"Error Creating Telecaster Forwarding Endpoint",
			fmt.Sprintf("Unexpected type %T when waiting for forwarding endpoint to become active", rawEndpoint),
		)
		return
	}

	data.Set(ctx, endpoint)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (f *ForwardingEndpointResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var ref model.ForwardingEndpointRefModel
	resp.Diagnostics.Append(data.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	refMsg, diags := ref.ToMsg()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := f.Client.DeleteEndpoint(ctx, connect.NewRequest(&clusterv1beta1.DeleteEndpointRequest{
		Ref: refMsg,
	})); err != nil {
		if coreweave.IsNotFoundError(err) {
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	const stateDeleted = "deleted"

	pollConf := retry.StateChangeConf{
		Pending: []string{
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{
			stateDeleted,
		},
		Refresh: func() (any, string, error) {
			result, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: refMsg,
			}))
			if err != nil {
				return nil, telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			return result.Msg.Endpoint, result.Msg.Endpoint.Status.State.String(), nil
		},
		Timeout: 10 * time.Minute,
	}

	_, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
}

func (f *ForwardingEndpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var ref model.ForwardingEndpointRefModel
	resp.Diagnostics.Append(data.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	refMsg, diags := ref.ToMsg()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
		Ref: refMsg,
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(ctx, getResp.Msg.Endpoint)

	tflog.Debug(ctx, "Reading Telecaster Forwarding Endpoint", map[string]any{
		"ref":    data.Ref.String(),
		"spec":   data.Spec.String(),
		"status": data.Status.String(),
	})
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (f *ForwardingEndpointResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointProto, diags := data.ToMsg(ctx)
	if diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	if _, err := f.Client.UpdateEndpoint(ctx, connect.NewRequest(&clusterv1beta1.UpdateEndpointRequest{
		Ref:  endpointProto.Ref,
		Spec: endpointProto.Spec,
	})); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	pollConf := retry.StateChangeConf{
		Pending: []string{},
		Target: []string{
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: endpointProto.Ref,
			}))
			if err != nil {
				return nil, telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			return getResp.Msg.Endpoint, getResp.Msg.Endpoint.Status.State.String(), nil
		},
		Timeout: 10 * time.Minute,
	}

	rawEndpoint, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	endpoint, ok := rawEndpoint.(*telecastertypesv1beta1.ForwardingEndpoint)
	if !ok {
		resp.Diagnostics.AddError(
			"Error Updating Telecaster Forwarding Endpoint",
			fmt.Sprintf("Unexpected type %T when waiting for forwarding endpoint to become active", rawEndpoint),
		)
		return
	}

	resp.Diagnostics.Append(data.Set(ctx, endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

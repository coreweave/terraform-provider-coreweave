package telecaster

import (
	"context"
	"fmt"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

var (
	_ resource.ResourceWithConfigure        = &ForwardingEndpointResource{}
	_ resource.ResourceWithValidateConfig   = &ForwardingEndpointResource{}
	_ resource.ResourceWithConfigValidators = &ForwardingEndpointResource{}
	_ resource.ResourceWithModifyPlan       = &ForwardingEndpointResource{}
	_ resource.ResourceWithImportState      = &ForwardingEndpointResource{}
)

func NewForwardingEndpointResource() resource.Resource {
	return &ForwardingEndpointResource{}
}

type ForwardingEndpointResource struct {
	coretf.CoreResource
}

type ForwardingEndpointResourceModel struct {
	Ref         types.Object `tfsdk:"ref"`
	Spec        types.Object `tfsdk:"spec"`
	Credentials types.Object `tfsdk:"credentials"`
	Status      types.Object `tfsdk:"status"`
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

func (e *ForwardingEndpointResourceModel) Set(ctx context.Context, data *typesv1beta1.ForwardingEndpoint) (diagnostics diag.Diagnostics) {
	var ref model.ForwardingEndpointRefModel
	// calling .As ensures that any nested `types.Object`s are properly initialized.
	diagnostics.Append(e.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(ref.Set(data.Ref)...)
	refObj, diags := types.ObjectValueFrom(ctx, e.Ref.AttributeTypes(ctx), &ref)
	diagnostics.Append(diags...)
	e.Ref = refObj

	var spec model.ForwardingEndpointSpecModel
	diagnostics.Append(e.Spec.As(ctx, &spec, basetypes.ObjectAsOptions{})...)
	if diagnostics.HasError() {
		return
	}
	diagnostics.Append(spec.Set(data.Spec)...)
	specObj, diags := types.ObjectValueFrom(ctx, e.Spec.AttributeTypes(ctx), &spec)
	diagnostics.Append(diags...)
	e.Spec = specObj

	if data.Status != nil {
		var status model.ForwardingEndpointStatusModel
		diagnostics.Append(e.Status.As(ctx, &status, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true})...)
		if diagnostics.HasError() {
			return
		}
		diagnostics.Append(status.Set(data.Status)...)
		statusObj, diags := types.ObjectValueFrom(ctx, e.Status.AttributeTypes(ctx), &status)
		diagnostics.Append(diags...)
		e.Status = statusObj
	}

	return
}

func (f *ForwardingEndpointResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data ForwardingEndpointResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	msg, diags := data.ToMsg(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := protovalidate.Validate(msg); err != nil {
		resp.Diagnostics.AddError("Validation Error", err.Error())
	}
}

func (f *ForwardingEndpointResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	spec := path.MatchRoot("spec")

	credentials := path.MatchRoot("credentials")

	return []resource.ConfigValidator{
		// Exactly one endpoint type must be configured
		resourcevalidator.ExactlyOneOf(
			spec.AtName("https"),
			spec.AtName("prometheus"),
			spec.AtName("s3"),
		),

		// Credentials are mutually exclusive, but not required.
		resourcevalidator.Conflicting(
			credentials.AtName("prometheus"),
			credentials.AtName("https"),
			credentials.AtName("s3"),
		),

		resourcevalidator.Conflicting(spec.AtName("prometheus"), credentials.AtName("https")),
		resourcevalidator.Conflicting(spec.AtName("prometheus"), credentials.AtName("s3")),
		resourcevalidator.Conflicting(spec.AtName("s3"), credentials.AtName("prometheus")),
		resourcevalidator.Conflicting(spec.AtName("s3"), credentials.AtName("https")),
		resourcevalidator.Conflicting(spec.AtName("https"), credentials.AtName("prometheus")),
		resourcevalidator.Conflicting(spec.AtName("https"), credentials.AtName("s3")),
	}
}

func (f *ForwardingEndpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ref").AtName("slug"), req, resp)
}

// endpointRequestWithCredentials is an interface for requests that can have credentials set.
// Both CreateEndpointRequest and UpdateEndpointRequest implement these methods.
type endpointRequestWithCredentials interface {
	SetPrometheus(*typesv1beta1.PrometheusCredentials)
	SetHttps(*typesv1beta1.HTTPSCredentials)
	SetS3(*typesv1beta1.S3Credentials)
}

// applyCredentials extracts credentials from the resource model and applies them to the request.
// Returns diagnostics if any errors occur during credential extraction or conversion.
func applyCredentials(ctx context.Context, data *ForwardingEndpointResourceModel, req endpointRequestWithCredentials) (diagnostics diag.Diagnostics) {
	if data.Credentials.IsNull() || data.Credentials.IsUnknown() {
		return nil
	}

	var credentials model.ForwardingEndpointCredentialsModel
	diagnostics.Append(data.Credentials.As(ctx, &credentials, basetypes.ObjectAsOptions{})...)
	if diagnostics.HasError() {
		return
	}

	if credentials.Prometheus != nil {
		promCreds, diags := credentials.Prometheus.ToMsg()
		diagnostics.Append(diags...)
		if diagnostics.HasError() {
			return
		}
		req.SetPrometheus(promCreds)
	} else if credentials.HTTPS != nil {
		httpsCreds, diags := credentials.HTTPS.ToMsg()
		diagnostics.Append(diags...)
		if diagnostics.HasError() {
			return
		}
		req.SetHttps(httpsCreds)
	} else if credentials.S3 != nil {
		s3Creds, diags := credentials.S3.ToMsg()
		diagnostics.Append(diags...)
		if diagnostics.HasError() {
			return
		}
		req.SetS3(s3Creds)
	}

	return
}

func (f *ForwardingEndpointResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	var data ForwardingEndpointResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var spec model.ForwardingEndpointSpecModel
	resp.Diagnostics.Append(data.Spec.As(ctx, &spec, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	if spec.S3 != nil {
		spec.S3.RequiresCredentials = types.BoolValue(!data.Credentials.IsNull())
	}

	specObj, diags := types.ObjectValueFrom(ctx, data.Spec.AttributeTypes(ctx), &spec)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Spec = specObj

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &data)...)
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
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
				},
			},
			"spec": schema.SingleNestedAttribute{
				MarkdownDescription: "The specification for the forwarding endpoint.",
				Required:            true,
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplaceIf(func(ctx context.Context, or planmodifier.ObjectRequest, rrifr *objectplanmodifier.RequiresReplaceIfFuncResponse) {
						if or.StateValue.IsNull() || or.PlanValue.IsUnknown() || or.ConfigValue.IsUnknown() {
							return
						}

						var (
							prior   model.ForwardingEndpointSpecModel
							planned model.ForwardingEndpointSpecModel
						)
						rrifr.Diagnostics.Append(or.StateValue.As(ctx, &prior, basetypes.ObjectAsOptions{})...)
						rrifr.Diagnostics.Append(or.PlanValue.As(ctx, &planned, basetypes.ObjectAsOptions{})...)
						if rrifr.Diagnostics.HasError() {
							return
						}

						// Render them to messages so we can easily detect the type
						priorMsg, diags := prior.ToMsg()
						rrifr.Diagnostics.Append(diags...)
						plannedMsg, diags := planned.ToMsg()
						rrifr.Diagnostics.Append(diags...)
						if rrifr.Diagnostics.HasError() {
							return
						}

						rrifr.RequiresReplace = plannedMsg.WhichConfig() != priorMsg.WhichConfig()
					},
						"If the type of the forwarding endpoint is configured and changes, Terraform will destroy and recreate the resource.",
						"If the type of the forwarding endpoint is configured and changes, Terraform will destroy and recreate the resource."),
				},
				Attributes: map[string]schema.Attribute{
					"display_name": schema.StringAttribute{
						MarkdownDescription: "The display name of the forwarding endpoint.",
						Required:            true,
					},
					"prometheus": schema.SingleNestedAttribute{
						MarkdownDescription: "Prometheus forwarding endpoint configuration.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"endpoint": schema.StringAttribute{
								MarkdownDescription: "The Prometheus remote write endpoint.",
								Required:            true,
							},
							"tls": tlsConfigAttribute(),
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
								MarkdownDescription: "Whether the S3 endpoint requires credentials. This is detected automatically based on the presence of credentials.",
								Computed:            true,
							},
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
							"tls": tlsConfigAttribute(),
						},
					},
				},
			},
			"credentials": schema.SingleNestedAttribute{
				MarkdownDescription: "Authentication credentials for the forwarding endpoint. The credential type must match the endpoint type configured in spec.",
				Optional:            true,
				Validators:          []validator.Object{},
				Attributes: map[string]schema.Attribute{
					"prometheus": schema.SingleNestedAttribute{
						MarkdownDescription: "Prometheus Remote Write authentication credentials.",
						Optional:            true,
						Validators: []validator.Object{
							objectvalidator.ExactlyOneOf(
								path.MatchRoot("credentials").AtName("prometheus"),
								path.MatchRoot("credentials").AtName("https"),
								path.MatchRoot("credentials").AtName("s3"),
							),
						},
						Attributes: map[string]schema.Attribute{
							"basic_auth":   basicAuthAttribute(),
							"bearer_token": bearerTokenAttribute(),
							"auth_headers": authHeadersAttribute(),
						},
					},
					"https": schema.SingleNestedAttribute{
						MarkdownDescription: "HTTPS endpoint authentication credentials.",
						Optional:            true,
						Validators:          []validator.Object{},
						Attributes: map[string]schema.Attribute{
							"basic_auth":   basicAuthAttribute(),
							"bearer_token": bearerTokenAttribute(),
							"auth_headers": authHeadersAttribute(),
						},
					},
					"s3": schema.SingleNestedAttribute{
						MarkdownDescription: "AWS S3 authentication credentials.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"access_key_id": schema.StringAttribute{
								MarkdownDescription: "AWS access key ID.",
								Required:            true,
								WriteOnly:           true,
								Sensitive:           true,
							},
							"secret_access_key": schema.StringAttribute{
								MarkdownDescription: "AWS secret access key.",
								Required:            true,
								WriteOnly:           true,
								Sensitive:           true,
							},
							"session_token": schema.StringAttribute{
								MarkdownDescription: "AWS session token for temporary credentials (optional).",
								Optional:            true,
								WriteOnly:           true,
								Sensitive:           true,
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

	createReq := &clusterv1beta1.CreateEndpointRequest{
		Ref:  endpointMsg.Ref,
		Spec: endpointMsg.Spec,
	}

	// Add credentials if provided
	resp.Diagnostics.Append(applyCredentials(ctx, &data, createReq)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := f.Client.CreateEndpoint(ctx, connect.NewRequest(createReq)); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	pollConf := retry.StateChangeConf{
		Pending: []string{
			// typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: endpointMsg.Ref,
			}))
			if err != nil {
				return nil, typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
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

	endpoint, ok := rawEndpoint.(*typesv1beta1.ForwardingEndpoint)
	if !ok {
		resp.Diagnostics.AddError(
			"Error Creating Telecaster Forwarding Endpoint",
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
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	pollConf := retry.StateChangeConf{
		Pending: []string{
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{},
		Refresh: func() (any, string, error) {
			result, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: refMsg,
			}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					return nil, "", nil
				}
				return nil, typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
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

	updateReq := &clusterv1beta1.UpdateEndpointRequest{
		Ref:  endpointProto.Ref,
		Spec: endpointProto.Spec,
	}

	// Add credentials if provided
	resp.Diagnostics.Append(applyCredentials(ctx, &data, updateReq)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := f.Client.UpdateEndpoint(ctx, connect.NewRequest(updateReq)); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	pollConf := retry.StateChangeConf{
		Pending: []string{},
		Target: []string{
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: endpointProto.Ref,
			}))
			if err != nil {
				return nil, typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
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

	endpoint, ok := rawEndpoint.(*typesv1beta1.ForwardingEndpoint)
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

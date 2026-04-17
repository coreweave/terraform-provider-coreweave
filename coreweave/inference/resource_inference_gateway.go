package inference

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

var (
	_ resource.Resource                     = &InferenceGatewayResource{}
	_ resource.ResourceWithImportState      = &InferenceGatewayResource{}
	_ resource.ResourceWithConfigValidators = &InferenceGatewayResource{}

	errGatewayFailed = errors.New("inference gateway entered a failed state")

	// validAPITypes is derived from the proto enum map so it stays in sync with the proto definition.
	validAPITypes = func() []string {
		vals := make([]string, 0, len(inferencev1.BodyBasedRouting_APIType_name)-1)
		for k := range inferencev1.BodyBasedRouting_APIType_name {
			if k == 0 { // skip UNSPECIFIED
				continue
			}
			s := apiTypeToString(inferencev1.BodyBasedRouting_APIType(k))
			if s != "" {
				vals = append(vals, s)
			}
		}
		slices.Sort(vals)
		return vals
	}()
)

func NewInferenceGatewayResource() resource.Resource {
	return &InferenceGatewayResource{}
}

type InferenceGatewayResource struct {
	client *coreweave.InferenceClient
}

// Nested model types.

type CoreWeaveAuthModel struct{}

type WeightsAndBiasesAuthModel struct {
	APIKey             types.String `tfsdk:"api_key"`
	ServerURL          types.String `tfsdk:"server_url"`
	EnableUsageReports types.Bool   `tfsdk:"enable_usage_reports"`
	EnableRateLimiting types.Bool   `tfsdk:"enable_rate_limiting"`
}

type GatewayAuthModel struct {
	CoreWeave        *CoreWeaveAuthModel        `tfsdk:"core_weave"`
	WeightsAndBiases *WeightsAndBiasesAuthModel `tfsdk:"weights_and_biases"`
}

type BodyBasedRoutingModel struct {
	APIType types.String `tfsdk:"api_type"`
}

type HeaderBasedRoutingModel struct {
	HeaderName types.String `tfsdk:"header_name"`
}

type PathBasedRoutingModel struct{}

type GatewayRoutingModel struct {
	BodyBased   *BodyBasedRoutingModel   `tfsdk:"body_based"`
	HeaderBased *HeaderBasedRoutingModel `tfsdk:"header_based"`
	PathBased   *PathBasedRoutingModel   `tfsdk:"path_based"`
}

type EndpointConfigurationModel struct {
	AdditionalDNS types.List `tfsdk:"additional_dns"`
}

// InferenceGatewayResourceModel is the top-level Terraform state model.
type InferenceGatewayResourceModel struct {
	// Computed
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	Status         types.String `tfsdk:"status"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
	Conditions     types.List   `tfsdk:"conditions"`
	Endpoints      types.List   `tfsdk:"endpoints"`
	// Required / Optional
	Name                  types.String                `tfsdk:"name"`
	Zones                 types.Set                   `tfsdk:"zones"`
	Auth                  GatewayAuthModel            `tfsdk:"auth"`
	Routing               GatewayRoutingModel         `tfsdk:"routing"`
	EndpointConfiguration *EndpointConfigurationModel `tfsdk:"endpoint_configuration"`
}

func (r *InferenceGatewayResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inference_gateway"
}

func (r *InferenceGatewayResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Create and manage [CoreWeave Managed Inference](https://docs.coreweave.com/products/inference) gateways.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The unique identifier of the gateway.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"organization_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The organization ID that owns the gateway.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The current status of the gateway.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of when the gateway was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of when the gateway was last updated.",
			},
			"conditions": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Detailed status conditions for the gateway.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type":             schema.StringAttribute{Computed: true},
						"status":           schema.StringAttribute{Computed: true},
						"last_update_time": schema.StringAttribute{Computed: true},
						"reason":           schema.StringAttribute{Computed: true},
						"message":          schema.StringAttribute{Computed: true},
					},
				},
			},
			"endpoints": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The endpoint URIs for the gateway.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The human-readable name of the gateway.",
			},
			"zones": schema.SetAttribute{
				ElementType:         types.StringType,
				Required:            true,
				MarkdownDescription: "The zones to make the gateway available in. Limits where deployments associated with the gateway may exist.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
				},
			},
			"auth": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "The authentication configuration for the gateway. Exactly one of `core_weave` or `weights_and_biases` must be specified.",
				Attributes: map[string]schema.Attribute{
					"core_weave": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Use CoreWeave IAM authentication.",
						Attributes:          map[string]schema.Attribute{},
					},
					"weights_and_biases": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Use Weights & Biases authentication.",
						Attributes: map[string]schema.Attribute{
							"api_key": schema.StringAttribute{
								Optional:            true,
								Sensitive:           true,
								MarkdownDescription: "The organization API key for Weights & Biases. Required if `server_url` is set.",
							},
							"server_url": schema.StringAttribute{
								Optional:            true,
								MarkdownDescription: "The Weights & Biases server URL. Defaults to the shared SaaS instance if not set.",
							},
							"enable_usage_reports": schema.BoolAttribute{
								Optional:            true,
								MarkdownDescription: "Whether to send usage data to Weights & Biases.",
							},
							"enable_rate_limiting": schema.BoolAttribute{
								Optional:            true,
								MarkdownDescription: "Whether to enable Weights & Biases controlled rate limiting.",
							},
						},
					},
				},
			},
			"routing": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "The routing configuration for the gateway. Exactly one of `body_based`, `header_based`, or `path_based` must be specified.",
				Attributes: map[string]schema.Attribute{
					"body_based": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Body-based routing configuration. Routes requests based on the request body content.",
						Attributes: map[string]schema.Attribute{
							"api_type": schema.StringAttribute{
								Required:            true,
								MarkdownDescription: fmt.Sprintf("The well-known API type for routing. Must be one of: %s.", strings.Join(validAPITypes, ", ")),
								Validators: []validator.String{
									stringvalidator.OneOf(validAPITypes...),
								},
							},
						},
					},
					"header_based": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Header-based routing configuration. Routes requests using a header value to match the model by name.",
						Attributes: map[string]schema.Attribute{
							"header_name": schema.StringAttribute{
								Required:            true,
								MarkdownDescription: "The name of the header to use for routing.",
								Validators: []validator.String{
									stringvalidator.LengthBetween(1, 100),
								},
							},
						},
					},
					"path_based": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Path-based routing configuration. Routes requests based on the URL path.",
						Attributes:          map[string]schema.Attribute{},
					},
				},
			},
			"endpoint_configuration": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Additional endpoint configuration options.",
				Attributes: map[string]schema.Attribute{
					"additional_dns": schema.ListAttribute{
						ElementType:         types.StringType,
						Optional:            true,
						MarkdownDescription: "Additional DNS names for the gateway endpoint. These DNS names must be manually configured to point to the gateway endpoint.",
					},
				},
			},
		},
	}
}

func (r *InferenceGatewayResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(
			path.MatchRoot("auth").AtName("core_weave"),
			path.MatchRoot("auth").AtName("weights_and_biases"),
		),
		resourcevalidator.ExactlyOneOf(
			path.MatchRoot("routing").AtName("body_based"),
			path.MatchRoot("routing").AtName("header_based"),
			path.MatchRoot("routing").AtName("path_based"),
		),
	}
}

func (r *InferenceGatewayResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *coreweave.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = client.Inference
}

func (r *InferenceGatewayResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data InferenceGatewayResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq, diags := toCreateGatewayRequest(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createResp, err := r.client.CreateGateway(ctx, connect.NewRequest(createReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// Save initial state before polling so the resource is tracked even if polling fails.
	resp.Diagnostics.Append(setFromGateway(&data, createResp.Msg.Gateway)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gatewayID := createResp.Msg.Gateway.GetSpec().GetId()

	// TODO: follow up with the managed inference team to validate that STATUS_UNSPECIFIED
	// is the correct "ready" target state for gateways, since the proto has no STATUS_READY enum value.
	conf := retry.StateChangeConf{
		Pending: []string{
			inferencev1.GatewayStatus_STATUS_CREATING.String(),
		},
		Target: []string{inferencev1.GatewayStatus_STATUS_UNSPECIFIED.String()},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetGateway(ctx, connect.NewRequest(&inferencev1.GetGatewayRequest{
				Id: gatewayID,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to poll gateway", map[string]interface{}{"error": err})
				return nil, inferencev1.GatewayStatus_STATUS_UNSPECIFIED.String(), err
			}
			gw := getResp.Msg.Gateway
			status := gw.GetStatus().GetStatus()
			if status == inferencev1.GatewayStatus_STATUS_ERROR || status == inferencev1.GatewayStatus_STATUS_FAILED {
				return gw, status.String(), errGatewayFailed
			}
			return gw, status.String(), nil
		},
		Timeout:    45 * time.Minute,
		MinTimeout: 5 * time.Second,
	}

	raw, err := conf.WaitForStateContext(ctx)
	if err != nil && !errors.Is(err, errGatewayFailed) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	gw, ok := raw.(*inferencev1.Gateway)
	if !ok {
		resp.Diagnostics.AddError("Unexpected polling result type", "Expected *inferencev1.Gateway. Please report this issue to the provider developers.")
		return
	}

	if gw.GetStatus().GetStatus() == inferencev1.GatewayStatus_STATUS_ERROR ||
		gw.GetStatus().GetStatus() == inferencev1.GatewayStatus_STATUS_FAILED {
		resp.Diagnostics.AddError("Gateway creation failed",
			fmt.Sprintf("Gateway entered status %s. You must destroy and recreate this resource.", gw.GetStatus().GetStatus().String()))
	}

	resp.Diagnostics.Append(setFromGateway(&data, gw)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InferenceGatewayResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data InferenceGatewayResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := r.client.GetGateway(ctx, connect.NewRequest(&inferencev1.GetGatewayRequest{
		Id: data.ID.ValueString(),
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(setFromGateway(&data, getResp.Msg.Gateway)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InferenceGatewayResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data InferenceGatewayResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateReq, diags := toUpdateGatewayRequest(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateResp, err := r.client.UpdateGateway(ctx, connect.NewRequest(updateReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	gatewayID := updateResp.Msg.Gateway.GetSpec().GetId()

	// TODO: follow up with the managed inference team to validate that STATUS_UNSPECIFIED
	// is the correct "ready" target state for gateways, since the proto has no STATUS_READY enum value.
	conf := retry.StateChangeConf{
		Pending: []string{
			inferencev1.GatewayStatus_STATUS_UPDATING.String(),
			inferencev1.GatewayStatus_STATUS_CREATING.String(),
		},
		Target: []string{inferencev1.GatewayStatus_STATUS_UNSPECIFIED.String()},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetGateway(ctx, connect.NewRequest(&inferencev1.GetGatewayRequest{
				Id: gatewayID,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to poll gateway", map[string]interface{}{"error": err.Error()})
				return nil, inferencev1.GatewayStatus_STATUS_UNSPECIFIED.String(), err
			}
			gw := getResp.Msg.Gateway
			status := gw.GetStatus().GetStatus()
			if status == inferencev1.GatewayStatus_STATUS_ERROR || status == inferencev1.GatewayStatus_STATUS_FAILED {
				return gw, status.String(), errGatewayFailed
			}
			return gw, status.String(), nil
		},
		Timeout:    20 * time.Minute,
		MinTimeout: 5 * time.Second,
	}

	raw, err := conf.WaitForStateContext(ctx)
	if err != nil && !errors.Is(err, errGatewayFailed) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	gw, ok := raw.(*inferencev1.Gateway)
	if !ok {
		resp.Diagnostics.AddError("Unexpected polling result type", "Expected *inferencev1.Gateway. Please report this issue to the provider developers.")
		return
	}

	if gw.GetStatus().GetStatus() == inferencev1.GatewayStatus_STATUS_ERROR ||
		gw.GetStatus().GetStatus() == inferencev1.GatewayStatus_STATUS_FAILED {
		resp.Diagnostics.AddError("Gateway update failed",
			fmt.Sprintf("Gateway entered status %s. Check the `conditions` attribute for details.", gw.GetStatus().GetStatus().String()))
	}

	resp.Diagnostics.Append(setFromGateway(&data, gw)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InferenceGatewayResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data InferenceGatewayResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gatewayID := data.ID.ValueString()

	_, err := r.client.DeleteGateway(ctx, connect.NewRequest(&inferencev1.DeleteGatewayRequest{
		Id: gatewayID,
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// deletedState is a synthetic target state returned when CodeNotFound is received,
	// since the inference proto has no STATUS_DELETED enum value.
	// TODO: follow up with the managed inference team to add STATUS_DELETED to the proto
	// so deletion can be polled deterministically via status rather than CodeNotFound.
	const deletedState = "NOT_FOUND"

	conf := retry.StateChangeConf{
		Pending: []string{
			inferencev1.GatewayStatus_STATUS_DELETING.String(),
			inferencev1.GatewayStatus_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{deletedState},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetGateway(ctx, connect.NewRequest(&inferencev1.GetGatewayRequest{
				Id: gatewayID,
			}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					return struct{}{}, deletedState, nil
				}
				tflog.Error(ctx, "failed to poll gateway deletion", map[string]interface{}{"error": err.Error()})
				return nil, inferencev1.GatewayStatus_STATUS_UNSPECIFIED.String(), err
			}
			gw := getResp.Msg.Gateway
			return gw, gw.GetStatus().GetStatus().String(), nil
		},
		Timeout:    20 * time.Minute,
		MinTimeout: 5 * time.Second,
	}

	_, err = conf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
}

func (r *InferenceGatewayResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- Helpers ---

// gatewayFields holds the fields shared between CreateGatewayRequest and UpdateGatewayRequest.
type gatewayFields struct {
	Name                  string
	Zones                 []string
	EndpointConfiguration *inferencev1.EndpointConfiguration
}

// buildGatewayFields extracts the common fields from the Terraform resource model.
func buildGatewayFields(ctx context.Context, m *InferenceGatewayResourceModel) (gatewayFields, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	zones := []string{}
	diagnostics.Append(m.Zones.ElementsAs(ctx, &zones, false)...)
	if diagnostics.HasError() {
		return gatewayFields{}, diagnostics
	}

	f := gatewayFields{
		Name:  m.Name.ValueString(),
		Zones: zones,
	}

	if m.EndpointConfiguration != nil {
		ec := &inferencev1.EndpointConfiguration{}
		if !m.EndpointConfiguration.AdditionalDNS.IsNull() && !m.EndpointConfiguration.AdditionalDNS.IsUnknown() {
			dns := []string{}
			diagnostics.Append(m.EndpointConfiguration.AdditionalDNS.ElementsAs(ctx, &dns, false)...)
			if diagnostics.HasError() {
				return gatewayFields{}, diagnostics
			}
			ec.AdditionalDns = dns
		}
		f.EndpointConfiguration = ec
	}

	return f, diagnostics
}

func toCreateGatewayRequest(ctx context.Context, m *InferenceGatewayResourceModel) (*inferencev1.CreateGatewayRequest, diag.Diagnostics) {
	f, diags := buildGatewayFields(ctx, m)
	if diags.HasError() {
		return nil, diags
	}

	req := &inferencev1.CreateGatewayRequest{
		Name:                  f.Name,
		Zones:                 f.Zones,
		EndpointConfiguration: f.EndpointConfiguration,
	}

	// Auth oneof
	if m.Auth.CoreWeave != nil {
		req.Auth = &inferencev1.CreateGatewayRequest_CoreWeaveAuth{
			CoreWeaveAuth: &inferencev1.CoreWeaveAuth{},
		}
	} else if m.Auth.WeightsAndBiases != nil {
		wb := &inferencev1.WeightsAndBiasesAuth{}
		if !m.Auth.WeightsAndBiases.APIKey.IsNull() && !m.Auth.WeightsAndBiases.APIKey.IsUnknown() {
			wb.ApiKey = m.Auth.WeightsAndBiases.APIKey.ValueString()
		}
		if !m.Auth.WeightsAndBiases.ServerURL.IsNull() && !m.Auth.WeightsAndBiases.ServerURL.IsUnknown() {
			wb.ServerUrl = m.Auth.WeightsAndBiases.ServerURL.ValueString()
		}
		if !m.Auth.WeightsAndBiases.EnableUsageReports.IsNull() && !m.Auth.WeightsAndBiases.EnableUsageReports.IsUnknown() {
			wb.EnableUsageReports = m.Auth.WeightsAndBiases.EnableUsageReports.ValueBool()
		}
		if !m.Auth.WeightsAndBiases.EnableRateLimiting.IsNull() && !m.Auth.WeightsAndBiases.EnableRateLimiting.IsUnknown() {
			wb.EnableRateLimiting = m.Auth.WeightsAndBiases.EnableRateLimiting.ValueBool()
		}
		req.Auth = &inferencev1.CreateGatewayRequest_WeightsAndBiasesAuth{
			WeightsAndBiasesAuth: wb,
		}
	}

	// Routing oneof
	switch {
	case m.Routing.BodyBased != nil:
		req.Routing = &inferencev1.CreateGatewayRequest_BodyBasedRouting{
			BodyBasedRouting: &inferencev1.BodyBasedRouting{
				ApiType: apiTypeFromString(m.Routing.BodyBased.APIType.ValueString()),
			},
		}
	case m.Routing.HeaderBased != nil:
		req.Routing = &inferencev1.CreateGatewayRequest_HeaderBasedRouting{
			HeaderBasedRouting: &inferencev1.HeaderBasedRouting{
				HeaderName: m.Routing.HeaderBased.HeaderName.ValueString(),
			},
		}
	case m.Routing.PathBased != nil:
		req.Routing = &inferencev1.CreateGatewayRequest_PathBasedRouting{
			PathBasedRouting: &inferencev1.PathBasedRouting{},
		}
	}

	return req, diags
}

func toUpdateGatewayRequest(ctx context.Context, m *InferenceGatewayResourceModel) (*inferencev1.UpdateGatewayRequest, diag.Diagnostics) {
	f, diags := buildGatewayFields(ctx, m)
	if diags.HasError() {
		return nil, diags
	}

	req := &inferencev1.UpdateGatewayRequest{
		Id:                    m.ID.ValueString(),
		Name:                  f.Name,
		Zones:                 f.Zones,
		EndpointConfiguration: f.EndpointConfiguration,
	}

	// Auth oneof
	if m.Auth.CoreWeave != nil {
		req.Auth = &inferencev1.UpdateGatewayRequest_CoreWeaveAuth{
			CoreWeaveAuth: &inferencev1.CoreWeaveAuth{},
		}
	} else if m.Auth.WeightsAndBiases != nil {
		wb := &inferencev1.WeightsAndBiasesAuth{}
		if !m.Auth.WeightsAndBiases.APIKey.IsNull() && !m.Auth.WeightsAndBiases.APIKey.IsUnknown() {
			wb.ApiKey = m.Auth.WeightsAndBiases.APIKey.ValueString()
		}
		if !m.Auth.WeightsAndBiases.ServerURL.IsNull() && !m.Auth.WeightsAndBiases.ServerURL.IsUnknown() {
			wb.ServerUrl = m.Auth.WeightsAndBiases.ServerURL.ValueString()
		}
		if !m.Auth.WeightsAndBiases.EnableUsageReports.IsNull() && !m.Auth.WeightsAndBiases.EnableUsageReports.IsUnknown() {
			wb.EnableUsageReports = m.Auth.WeightsAndBiases.EnableUsageReports.ValueBool()
		}
		if !m.Auth.WeightsAndBiases.EnableRateLimiting.IsNull() && !m.Auth.WeightsAndBiases.EnableRateLimiting.IsUnknown() {
			wb.EnableRateLimiting = m.Auth.WeightsAndBiases.EnableRateLimiting.ValueBool()
		}
		req.Auth = &inferencev1.UpdateGatewayRequest_WeightsAndBiasesAuth{
			WeightsAndBiasesAuth: wb,
		}
	}

	// Routing oneof
	switch {
	case m.Routing.BodyBased != nil:
		req.Routing = &inferencev1.UpdateGatewayRequest_BodyBasedRouting{
			BodyBasedRouting: &inferencev1.BodyBasedRouting{
				ApiType: apiTypeFromString(m.Routing.BodyBased.APIType.ValueString()),
			},
		}
	case m.Routing.HeaderBased != nil:
		req.Routing = &inferencev1.UpdateGatewayRequest_HeaderBasedRouting{
			HeaderBasedRouting: &inferencev1.HeaderBasedRouting{
				HeaderName: m.Routing.HeaderBased.HeaderName.ValueString(),
			},
		}
	case m.Routing.PathBased != nil:
		req.Routing = &inferencev1.UpdateGatewayRequest_PathBasedRouting{
			PathBasedRouting: &inferencev1.PathBasedRouting{},
		}
	}

	return req, diags
}

// setFromGateway populates all fields on the model from a proto Gateway response.
// For Optional (non-Computed) fields, null is preserved when the plan/state was null and the
// API returns the default (zero/empty) value.
//
//nolint:gocyclo
func setFromGateway(m *InferenceGatewayResourceModel, gw *inferencev1.Gateway) (diagnostics diag.Diagnostics) {
	spec := gw.GetSpec()
	status := gw.GetStatus()

	m.ID = types.StringValue(spec.GetId())
	m.OrganizationID = types.StringValue(spec.GetOrganizationId())
	m.Name = types.StringValue(spec.GetName())
	m.Status = types.StringValue(status.GetStatus().String())

	if status.GetCreatedAt() != nil {
		m.CreatedAt = types.StringValue(status.GetCreatedAt().AsTime().Format(time.RFC3339))
	}
	if status.GetUpdatedAt() != nil {
		m.UpdatedAt = types.StringValue(status.GetUpdatedAt().AsTime().Format(time.RFC3339))
	}

	// zones
	zoneVals := make([]attr.Value, 0, len(spec.GetZones()))
	for _, z := range spec.GetZones() {
		zoneVals = append(zoneVals, types.StringValue(z))
	}
	zoneSet, diags := types.SetValue(types.StringType, zoneVals)
	diagnostics.Append(diags...)
	m.Zones = zoneSet

	// auth — type switch on oneof
	switch spec.GetAuth().(type) {
	case *inferencev1.GatewaySpec_CoreWeaveAuth:
		m.Auth = GatewayAuthModel{
			CoreWeave:        &CoreWeaveAuthModel{},
			WeightsAndBiases: nil,
		}
	case *inferencev1.GatewaySpec_WeightsAndBiasesAuth:
		wb := spec.GetWeightsAndBiasesAuth()
		wbModel := &WeightsAndBiasesAuthModel{}

		// Null preservation: if state was null and API returns zero/empty, keep null.
		if m.Auth.WeightsAndBiases != nil && m.Auth.WeightsAndBiases.APIKey.IsNull() && wb.GetApiKey() == "" {
			wbModel.APIKey = types.StringNull()
		} else {
			wbModel.APIKey = types.StringValue(wb.GetApiKey())
		}

		if m.Auth.WeightsAndBiases != nil && m.Auth.WeightsAndBiases.ServerURL.IsNull() && wb.GetServerUrl() == "" {
			wbModel.ServerURL = types.StringNull()
		} else {
			wbModel.ServerURL = types.StringValue(wb.GetServerUrl())
		}

		if m.Auth.WeightsAndBiases != nil && m.Auth.WeightsAndBiases.EnableUsageReports.IsNull() && !wb.GetEnableUsageReports() {
			wbModel.EnableUsageReports = types.BoolNull()
		} else {
			wbModel.EnableUsageReports = types.BoolValue(wb.GetEnableUsageReports())
		}

		if m.Auth.WeightsAndBiases != nil && m.Auth.WeightsAndBiases.EnableRateLimiting.IsNull() && !wb.GetEnableRateLimiting() {
			wbModel.EnableRateLimiting = types.BoolNull()
		} else {
			wbModel.EnableRateLimiting = types.BoolValue(wb.GetEnableRateLimiting())
		}

		m.Auth = GatewayAuthModel{
			CoreWeave:        nil,
			WeightsAndBiases: wbModel,
		}
	default:
		// If the API returns an unrecognized auth variant, clear both.
		m.Auth = GatewayAuthModel{}
	}

	// routing — type switch on oneof
	switch spec.GetRouting().(type) {
	case *inferencev1.GatewaySpec_BodyBasedRouting:
		bbr := spec.GetBodyBasedRouting()
		m.Routing = GatewayRoutingModel{
			BodyBased: &BodyBasedRoutingModel{
				APIType: types.StringValue(apiTypeToString(bbr.GetApiType())),
			},
			HeaderBased: nil,
			PathBased:   nil,
		}
	case *inferencev1.GatewaySpec_HeaderBasedRouting:
		hbr := spec.GetHeaderBasedRouting()
		m.Routing = GatewayRoutingModel{
			BodyBased: nil,
			HeaderBased: &HeaderBasedRoutingModel{
				HeaderName: types.StringValue(hbr.GetHeaderName()),
			},
			PathBased: nil,
		}
	case *inferencev1.GatewaySpec_PathBasedRouting:
		m.Routing = GatewayRoutingModel{
			BodyBased:   nil,
			HeaderBased: nil,
			PathBased:   &PathBasedRoutingModel{},
		}
	default:
		m.Routing = GatewayRoutingModel{}
	}

	// endpoint_configuration — null preservation
	ec := spec.GetEndpointConfiguration()
	if m.EndpointConfiguration == nil && (ec == nil || len(ec.GetAdditionalDns()) == 0) {
		m.EndpointConfiguration = nil
	} else {
		ecModel := &EndpointConfigurationModel{}
		if ec != nil && len(ec.GetAdditionalDns()) > 0 {
			dnsVals := make([]attr.Value, 0, len(ec.GetAdditionalDns()))
			for _, d := range ec.GetAdditionalDns() {
				dnsVals = append(dnsVals, types.StringValue(d))
			}
			dnsList, diags := types.ListValue(types.StringType, dnsVals)
			diagnostics.Append(diags...)
			ecModel.AdditionalDNS = dnsList
		} else {
			ecModel.AdditionalDNS = types.ListNull(types.StringType)
		}
		m.EndpointConfiguration = ecModel
	}

	// endpoints
	epVals := make([]attr.Value, 0, len(status.GetEndpoints()))
	for _, ep := range status.GetEndpoints() {
		epVals = append(epVals, types.StringValue(ep))
	}
	epList, diags := types.ListValue(types.StringType, epVals)
	diagnostics.Append(diags...)
	m.Endpoints = epList

	// conditions
	condVals := make([]attr.Value, 0, len(status.GetConditions()))
	for _, c := range status.GetConditions() {
		lastUpdate := ""
		if c.GetLastUpdateTime() != nil {
			lastUpdate = c.GetLastUpdateTime().AsTime().Format(time.RFC3339)
		}
		condObj, diags := types.ObjectValue(conditionAttrTypes, map[string]attr.Value{
			"type":             types.StringValue(c.GetType()),
			"status":           types.StringValue(c.GetStatus().String()),
			"last_update_time": types.StringValue(lastUpdate),
			"reason":           types.StringValue(c.GetReason()),
			"message":          types.StringValue(c.GetMessage()),
		})
		diagnostics.Append(diags...)
		condVals = append(condVals, condObj)
	}
	condList, diags := types.ListValue(types.ObjectType{AttrTypes: conditionAttrTypes}, condVals)
	diagnostics.Append(diags...)
	m.Conditions = condList

	return diagnostics
}

// apiTypeFromString converts a user-facing API type string (e.g. "OPENAI") to the proto enum value.
func apiTypeFromString(s string) inferencev1.BodyBasedRouting_APIType {
	full := "API_TYPE_" + s
	if val, ok := inferencev1.BodyBasedRouting_APIType_value[full]; ok {
		return inferencev1.BodyBasedRouting_APIType(val)
	}
	return inferencev1.BodyBasedRouting_API_TYPE_UNSPECIFIED
}

// apiTypeToString converts a proto API type enum value to the user-facing string (e.g. "OPENAI").
func apiTypeToString(at inferencev1.BodyBasedRouting_APIType) string {
	s := at.String()
	return strings.TrimPrefix(s, "API_TYPE_")
}

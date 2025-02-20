//nolint:staticcheck
package networking

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/zclconf/go-cty/cty"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &VpcResource{}
	_ resource.ResourceWithImportState = &VpcResource{}
)

func NewVpcResource() resource.Resource {
	return &VpcResource{}
}

// VpcResource defines the resource implementation.
type VpcResource struct {
	client *coreweave.Client
}

type VpcDhcpDnsResourceModel struct {
	Servers types.Set `tfsdk:"servers"`
}

func (v *VpcDhcpDnsResourceModel) IsEmpty() bool {
	return v.Servers.IsNull() || len(v.Servers.Elements()) == 0
}

type VpcDhcpResourceModel struct {
	Dns *VpcDhcpDnsResourceModel `tfsdk:"dns"`
}

func (v *VpcDhcpResourceModel) IsEmpty() bool {
	if v.Dns == nil {
		return true
	}

	return v.Dns.IsEmpty()
}

func (v *VpcDhcpResourceModel) Set(dhcp *networkingv1beta1.DHCP) {
	if dhcp == nil {
		return
	}

	if dhcp.Dns != nil {
		v.Dns = &VpcDhcpDnsResourceModel{}

		servers := []attr.Value{}
		for _, s := range dhcp.Dns.Servers {
			servers = append(servers, types.StringValue(s))
		}
		ds := types.SetValueMust(types.StringType, servers)
		v.Dns.Servers = ds

		return
	}
}

type VpcPrefixResourceModel struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
}

func (v *VpcPrefixResourceModel) ToProto() *networkingv1beta1.Prefix {
	return &networkingv1beta1.Prefix{
		Name:  v.Name.ValueString(),
		Value: v.Value.ValueString(),
	}
}

func (v *VpcPrefixResourceModel) Set(prefix *networkingv1beta1.Prefix) {
	if prefix == nil {
		return
	}

	v.Name = types.StringValue(prefix.Name)
	v.Value = types.StringValue(prefix.Value)
}

type VpcIngressResourceModel struct {
	DisablePublicServices types.Bool `tfsdk:"disable_public_services"`
}

func (v *VpcIngressResourceModel) ToProto() *networkingv1beta1.Ingress {
	if v == nil {
		return nil
	}

	return &networkingv1beta1.Ingress{
		DisablePublicServices: v.DisablePublicServices.ValueBool(),
	}
}

type VpcEgressResourceModel struct {
	DisablePublicAccess types.Bool `tfsdk:"disable_public_access"`
}

func (v *VpcEgressResourceModel) ToProto() *networkingv1beta1.Egress {
	if v == nil {
		return nil
	}

	return &networkingv1beta1.Egress{
		DisablePublicAccess: v.DisablePublicAccess.ValueBool(),
	}
}

// VpcResourceModel describes the resource data model.
type VpcResourceModel struct {
	Id          types.String             `tfsdk:"id"`
	Zone        types.String             `tfsdk:"zone"`
	Name        types.String             `tfsdk:"name"`
	HostPrefix  types.String             `tfsdk:"host_prefix"`
	VpcPrefixes []VpcPrefixResourceModel `tfsdk:"vpc_prefixes"`
	Ingress     *VpcIngressResourceModel `tfsdk:"ingress"`
	Egress      *VpcEgressResourceModel  `tfsdk:"egress"`
	Dhcp        *VpcDhcpResourceModel    `tfsdk:"dhcp"`
}

func (v *VpcResourceModel) Set(vpc *networkingv1beta1.VPC) {
	if vpc == nil {
		return
	}

	v.Id = types.StringValue(vpc.Id)
	v.Name = types.StringValue(vpc.Name)
	v.Zone = types.StringValue(vpc.Zone)
	v.HostPrefix = types.StringValue(vpc.HostPrefix)
	v.Egress = &VpcEgressResourceModel{
		DisablePublicAccess: types.BoolValue(false),
	}

	if vpc.Ingress != nil {
		v.Ingress = &VpcIngressResourceModel{
			DisablePublicServices: types.BoolValue(vpc.Ingress.DisablePublicServices),
		}
	}

	if vpc.Egress != nil {
		v.Egress = &VpcEgressResourceModel{
			DisablePublicAccess: types.BoolValue(vpc.Egress.DisablePublicAccess),
		}
	}

	if len(vpc.VpcPrefixes) > 0 {
		vpcPrefixes := []VpcPrefixResourceModel{}
		for _, p := range vpc.VpcPrefixes {
			vp := VpcPrefixResourceModel{}
			vp.Set(p)
			vpcPrefixes = append(vpcPrefixes, vp)
		}
		v.VpcPrefixes = vpcPrefixes
	}

	dhcp := &VpcDhcpResourceModel{}
	dhcp.Set(vpc.Dhcp)
	if !dhcp.IsEmpty() {
		v.Dhcp = dhcp
	}
}

func (v *VpcResourceModel) GetDhcp(ctx context.Context) *networkingv1beta1.DHCP {
	if v.Dhcp == nil {
		return nil
	}

	ds := []string{}
	v.Dhcp.Dns.Servers.ElementsAs(ctx, &ds, true)

	dhcp := &networkingv1beta1.DHCP{
		Dns: &networkingv1beta1.DHCP_DNS{
			Servers: ds,
		},
	}

	return dhcp
}

func (v *VpcResourceModel) vpcPrefixes() []*networkingv1beta1.Prefix {
	vp := []*networkingv1beta1.Prefix{}
	for _, p := range v.VpcPrefixes {
		vp = append(vp, p.ToProto())
	}
	return vp
}

func (v *VpcResourceModel) ToCreateRequest(ctx context.Context) *networkingv1beta1.CreateVPCRequest {
	req := &networkingv1beta1.CreateVPCRequest{
		Name:        v.Name.ValueString(),
		Zone:        v.Zone.ValueString(),
		Ingress:     v.Ingress.ToProto(),
		Egress:      v.Egress.ToProto(),
		HostPrefix:  v.HostPrefix.ValueString(),
		VpcPrefixes: v.vpcPrefixes(),
		Dhcp:        v.GetDhcp(ctx),
	}

	// temporary until we deprecate the pubImport field
	if req.Ingress != nil {
		req.PubImport = !req.Ingress.DisablePublicServices
	} else {
		// default it to true so that ingress.disablePublicServices is false
		req.PubImport = true
	}

	return req
}

func (v *VpcResourceModel) ToUpdateRequest(ctx context.Context) *networkingv1beta1.UpdateVPCRequest {
	req := networkingv1beta1.UpdateVPCRequest{
		Id:          v.Id.ValueString(),
		VpcPrefixes: v.vpcPrefixes(),
		Dhcp:        v.GetDhcp(ctx),
		Ingress:     v.Ingress.ToProto(),
		Egress:      v.Egress.ToProto(),
	}

	// temporary until we deprecate the pubImport field
	if req.Ingress != nil {
		req.PubImport = !req.Ingress.DisablePublicServices
	} else {
		// default it to true so that ingress.disablePublicServices is false
		req.PubImport = true
	}

	return &req
}

func (r *VpcResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_networking_vpc"
}

func (r *VpcResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave VPC",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The unique identifier of the vpc.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"zone": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"ingress": schema.SingleNestedAttribute{
				Optional: true,
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"disable_public_services": schema.BoolAttribute{
						Optional: true,
					},
				},
				Default: objectdefault.StaticValue(types.ObjectValueMust(map[string]attr.Type{
					"disable_public_services": types.BoolType,
				}, map[string]attr.Value{
					"disable_public_services": types.BoolValue(false),
				})),
			},
			"egress": schema.SingleNestedAttribute{
				Optional: true,
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"disable_public_access": schema.BoolAttribute{
						Optional: true,
					},
				},
				Default: objectdefault.StaticValue(types.ObjectValueMust(map[string]attr.Type{
					"disable_public_access": types.BoolType,
				}, map[string]attr.Value{
					"disable_public_access": types.BoolValue(false),
				})),
			},
			"host_prefix": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIf(func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						// Skip if there's no prior state or if the config is unknown
						if req.StateValue.IsNull() || req.PlanValue.IsUnknown() || req.ConfigValue.IsUnknown() {
							return
						}

						if req.StateValue.ValueString() != req.PlanValue.ValueString() {
							resp.Diagnostics.AddWarning("host_prefix is immutable, changing this value will force a replacement", fmt.Sprintf("cannot change existing host_prefix '%s' to '%s'", req.StateValue.ValueString(), req.PlanValue.ValueString()))
						}

						if resp.Diagnostics.WarningsCount() > 0 {
							resp.RequiresReplace = true
						}
					}, "", ""),
				},
			},
			"vpc_prefixes": schema.SetNestedAttribute{
				Optional: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required: true,
						},
						"value": schema.StringAttribute{
							Required: true,
						},
					},
				},
			},
			"dhcp": schema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"dns": schema.SingleNestedAttribute{
						Optional: true,
						Attributes: map[string]schema.Attribute{
							"servers": schema.SetAttribute{
								Optional:    true,
								ElementType: types.StringType,
							},
						},
					},
				},
			},
		},
	}
}

func (r *VpcResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
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

	r.client = client
}

func (r *VpcResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data VpcResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	createResp, err := r.client.CreateVPC(ctx, connect.NewRequest(data.ToCreateRequest(ctx)))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// wait for the vpc to become ready
	conf := retry.StateChangeConf{
		Pending: []string{
			networkingv1beta1.VPC_STATUS_CREATING.String(),
			networkingv1beta1.VPC_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{networkingv1beta1.VPC_STATUS_READY.String()},
		Refresh: func() (result interface{}, state string, err error) {
			resp, err := r.client.GetVPC(ctx, connect.NewRequest(&networkingv1beta1.GetVPCRequest{
				Id: createResp.Msg.Vpc.Id,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to fetch vpc resource", map[string]interface{}{
					"error": err,
				})
				return nil, networkingv1beta1.VPC_STATUS_UNSPECIFIED.String(), err
			}

			return resp.Msg.Vpc, resp.Msg.Vpc.Status.String(), nil
		},
		Timeout: 20 * time.Minute,
	}

	rawVpc, err := conf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	vpc, ok := rawVpc.(*networkingv1beta1.VPC)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Create Type",
			"Expected *networkingv1beta1.VPC. Please report this issue to the provider developers.",
		)
		return
	}

	data.Set(vpc)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *VpcResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data VpcResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	vpc, err := r.client.GetVPC(ctx, connect.NewRequest(&networkingv1beta1.GetVPCRequest{
		Id: data.Id.ValueString(),
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(vpc.Msg.Vpc)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *VpcResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data VpcResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	updateResp, err := r.client.UpdateVPC(ctx, connect.NewRequest(data.ToUpdateRequest(ctx)))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// wait for the vpc to become ready
	conf := retry.StateChangeConf{
		Pending: []string{
			networkingv1beta1.VPC_STATUS_UPDATING.String(),
			networkingv1beta1.VPC_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{networkingv1beta1.VPC_STATUS_READY.String()},
		Refresh: func() (result interface{}, state string, err error) {
			resp, err := r.client.GetVPC(ctx, connect.NewRequest(&networkingv1beta1.GetVPCRequest{
				Id: updateResp.Msg.Vpc.Id,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to fetch vpc resource", map[string]interface{}{
					"error": err.Error(),
				})
				return nil, networkingv1beta1.VPC_STATUS_UNSPECIFIED.String(), err
			}

			tflog.Info(ctx, "fetching vpc", map[string]interface{}{
				"vpc": resp.Msg.Vpc.String(),
			})

			return resp.Msg.Vpc, resp.Msg.Vpc.Status.String(), nil
		},
		Timeout: 20 * time.Minute,
	}

	rawvpc, err := conf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	vpc, ok := rawvpc.(*networkingv1beta1.VPC)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Update Type",
			"Expected *networkingv1beta1.VPC. Please report this issue to the provider developers.",
		)
		return
	}

	data.Set(vpc)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *VpcResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data VpcResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteResp, err := r.client.DeleteVPC(ctx, connect.NewRequest(&networkingv1beta1.DeleteVPCRequest{
		Id: data.Id.ValueString(),
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	conf := retry.StateChangeConf{
		Pending: []string{
			networkingv1beta1.VPC_STATUS_DELETING.String(),
			networkingv1beta1.VPC_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{""},
		Refresh: func() (result interface{}, state string, err error) {
			resp, err := r.client.GetVPC(ctx, connect.NewRequest(&networkingv1beta1.GetVPCRequest{
				Id: deleteResp.Msg.Vpc.Id,
			}))
			if err != nil {
				var connectErr *connect.Error
				if errors.As(err, &connectErr) && connectErr.Code() == connect.CodeNotFound {
					return struct{}{}, "", nil
				}

				tflog.Error(ctx, "failed to fetch vpc", map[string]interface{}{
					"error": err.Error(),
				})
				return nil, networkingv1beta1.VPC_STATUS_UNSPECIFIED.String(), err
			}

			return resp.Msg.Vpc, resp.Msg.Vpc.Status.String(), nil
		},
		Timeout: 20 * time.Minute,
	}

	_, err = conf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
}

func (r *VpcResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// MustRenderVpcResource is a helper to render HCL for use in acceptance testing.
// It should not be used by clients of this library.
func MustRenderVpcResource(ctx context.Context, resourceName string, vpc *VpcResourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{"coreweave_networking_vpc", resourceName})
	resourceBody := resource.Body()

	resourceBody.SetAttributeValue("name", cty.StringVal(vpc.Name.ValueString()))
	resourceBody.SetAttributeValue("zone", cty.StringVal(vpc.Zone.ValueString()))

	if vpc.Ingress != nil {
		resourceBody.SetAttributeValue("ingress", cty.ObjectVal(map[string]cty.Value{
			"disable_public_services": cty.BoolVal(vpc.Ingress.DisablePublicServices.ValueBool()),
		}))
	}

	if vpc.Egress != nil {
		resourceBody.SetAttributeValue("egress", cty.ObjectVal(map[string]cty.Value{
			"disable_public_access": cty.BoolVal(vpc.Egress.DisablePublicAccess.ValueBool()),
		}))
	}

	if !vpc.HostPrefix.IsNull() {
		resourceBody.SetAttributeValue("host_prefix", cty.StringVal(vpc.HostPrefix.ValueString()))
	}

	vpcPrefixes := []cty.Value{}
	for _, p := range vpc.VpcPrefixes {
		vpcPrefixes = append(vpcPrefixes, cty.ObjectVal(map[string]cty.Value{
			"name":  cty.StringVal(p.Name.ValueString()),
			"value": cty.StringVal(p.Value.ValueString()),
		}))
	}

	if len(vpcPrefixes) > 0 {
		resourceBody.SetAttributeValue("vpc_prefixes", cty.ListVal(vpcPrefixes))
	}

	if vpc.Dhcp != nil {
		dhcp := map[string]cty.Value{}
		if vpc.Dhcp.Dns != nil {
			dns := map[string]cty.Value{}
			if !vpc.Dhcp.Dns.Servers.IsNull() {
				servers := []types.String{}
				vpc.Dhcp.Dns.Servers.ElementsAs(ctx, &servers, false)
				serverVals := []cty.Value{}
				for _, s := range servers {
					serverVals = append(serverVals, cty.StringVal(s.ValueString()))
				}

				if len(serverVals) > 0 {
					dns["servers"] = cty.SetVal(serverVals)
				}
			}
			if len(dns) > 0 {
				dhcp["dns"] = cty.ObjectVal(dns)
			}
		}

		if len(dhcp) > 0 {
			resourceBody.SetAttributeValue("dhcp", cty.ObjectVal(dhcp))
		}
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

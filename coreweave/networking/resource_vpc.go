//nolint:staticcheck
package networking

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework-nettypes/cidrtypes"
	"github.com/hashicorp/terraform-plugin-framework-nettypes/iptypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/zclconf/go-cty/cty"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                     = &VpcResource{}
	_ resource.ResourceWithImportState      = &VpcResource{}
	_ resource.ResourceWithConfigure        = &VpcResource{}
	_ resource.ResourceWithConfigValidators = &VpcResource{}
)

var hostPrefixObjectType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"name": types.StringType,
		"type": types.StringType,
		"prefixes": types.SetType{
			ElemType: types.StringType,
		},
		"ipam": types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"prefix_length":          types.Int32Type,
				"gateway_address_policy": types.StringType,
			},
		},
	},
}

func NewVpcResource() resource.Resource {
	return &VpcResource{}
}

// VpcResource defines the resource implementation.
type VpcResource struct {
	client *coreweave.Client
}

// ConfigValidators implements resource.ResourceWithConfigValidators.
func (r *VpcResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.Conflicting(path.MatchRoot("host_prefix"), path.MatchRoot("host_prefixes")),
	}
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

type HostPrefixResourceModel struct {
	Name     types.String            `tfsdk:"name"`
	Type     types.String            `tfsdk:"type"`
	Prefixes []types.String          `tfsdk:"prefixes"`
	IPAM     IPAMPolicyResourceModel `tfsdk:"ipam"`
}

func (hp *HostPrefixResourceModel) ToProto() *networkingv1beta1.HostPrefix {
	var hpType networkingv1beta1.HostPrefix_Type
	hpTypeVal := hp.Type.ValueString()
	if val, ok := networkingv1beta1.HostPrefix_Type_value[hpTypeVal]; ok {
		hpType = networkingv1beta1.HostPrefix_Type(val)
	}

	prefixes := make([]string, len(hp.Prefixes))
	for i, prefix := range hp.Prefixes {
		prefixes[i] = prefix.ValueString()
	}

	return &networkingv1beta1.HostPrefix{
		Name:     hp.Name.ValueString(),
		Type:     hpType,
		Prefixes: prefixes,
		Ipam:     hp.IPAM.ToProto(),
	}
}

type IPAMPolicyResourceModel struct {
	PrefixLength         types.Int32  `tfsdk:"prefix_length"`
	GatewayAddressPolicy types.String `tfsdk:"gateway_address_policy"`
}

func (ipam *IPAMPolicyResourceModel) ToProto() *networkingv1beta1.IPAddressManagementPolicy {
	var gwPolicy networkingv1beta1.IPAddressManagementPolicy_GatewayAddressPolicy
	gwPolVal := ipam.GatewayAddressPolicy.ValueString()
	if val, ok := networkingv1beta1.IPAddressManagementPolicy_GatewayAddressPolicy_value[gwPolVal]; ok {
		gwPolicy = networkingv1beta1.IPAddressManagementPolicy_GatewayAddressPolicy(val)
	}

	return &networkingv1beta1.IPAddressManagementPolicy{
		PrefixLength:         ipam.PrefixLength.ValueInt32(),
		GatewayAddressPolicy: gwPolicy,
	}
}

func (hp *HostPrefixResourceModel) Set(prefix *networkingv1beta1.HostPrefix) {
	if hp == nil {
		return
	}

	var hpType types.String
	hpTypeNum := int32(prefix.Type)
	if val, ok := networkingv1beta1.HostPrefix_Type_name[hpTypeNum]; ok {
		hpType = types.StringValue(val)
	}

	prefixes := []types.String{}
	for _, p := range prefix.Prefixes {
		prefixes = append(prefixes, types.StringValue(p))
	}

	var gwPolicy types.String
	gwPolicyNum := int32(prefix.Ipam.GetGatewayAddressPolicy())
	if val, ok := networkingv1beta1.IPAddressManagementPolicy_GatewayAddressPolicy_name[gwPolicyNum]; ok {
		gwPolicy = types.StringValue(val)
	}

	ipam := IPAMPolicyResourceModel{
		PrefixLength:         types.Int32Value(prefix.Ipam.GetPrefixLength()),
		GatewayAddressPolicy: gwPolicy,
	}

	hp.Name = types.StringValue(prefix.Name)
	hp.Prefixes = prefixes
	hp.Type = hpType
	hp.IPAM = ipam
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
	Id           types.String             `tfsdk:"id"`
	Zone         types.String             `tfsdk:"zone"`
	Name         types.String             `tfsdk:"name"`
	HostPrefix   types.String             `tfsdk:"host_prefix"`
	HostPrefixes types.Set                `tfsdk:"host_prefixes"`
	VpcPrefixes  []VpcPrefixResourceModel `tfsdk:"vpc_prefixes"`
	Ingress      *VpcIngressResourceModel `tfsdk:"ingress"`
	Egress       *VpcEgressResourceModel  `tfsdk:"egress"`
	Dhcp         *VpcDhcpResourceModel    `tfsdk:"dhcp"`
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

	if len(vpc.HostPrefixes) > 0 {
		hostPrefixes := []HostPrefixResourceModel{}
		for _, p := range vpc.HostPrefixes {
			hp := HostPrefixResourceModel{}
			hp.Set(p)
			hostPrefixes = append(hostPrefixes, hp)
		}

		setVal, diags := types.SetValueFrom(
			context.Background(),
			hostPrefixObjectType,
			hostPrefixes,
		)
		if diags.HasError() {
			v.HostPrefixes = types.SetNull(hostPrefixObjectType)
		} else {
			v.HostPrefixes = setVal
		}
	} else {
		v.HostPrefixes = types.SetNull(hostPrefixObjectType)
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

	dhcp := VpcDhcpResourceModel{}
	dhcp.Set(vpc.Dhcp)
	// if there is any dhcp config returned from the API, set it
	if !dhcp.IsEmpty() {
		v.Dhcp = &dhcp
	} else { // otherwise, remove it
		v.Dhcp = nil
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

func (v *VpcResourceModel) hostPrefixes(ctx context.Context) []*networkingv1beta1.HostPrefix {
	if v.HostPrefixes.IsNull() || v.HostPrefixes.IsUnknown() {
		return nil
	}

	var models []HostPrefixResourceModel
	diags := v.HostPrefixes.ElementsAs(ctx, &models, false)
	if diags.HasError() {
		return nil
	}

	hp := make([]*networkingv1beta1.HostPrefix, len(models))
	for i, m := range models {
		hp[i] = m.ToProto()
	}

	return hp
}

func (v *VpcResourceModel) vpcPrefixes() []*networkingv1beta1.Prefix {
	vp := make([]*networkingv1beta1.Prefix, len(v.VpcPrefixes))
	for i, p := range v.VpcPrefixes {
		vp[i] = p.ToProto()
	}
	return vp
}

func (v *VpcResourceModel) ToCreateRequest(ctx context.Context) *networkingv1beta1.CreateVPCRequest {
	req := &networkingv1beta1.CreateVPCRequest{
		Name:         v.Name.ValueString(),
		Zone:         v.Zone.ValueString(),
		VpcPrefixes:  v.vpcPrefixes(),
		HostPrefix:   v.HostPrefix.ValueString(),
		HostPrefixes: v.hostPrefixes(ctx),
		Ingress:      v.Ingress.ToProto(),
		Egress:       v.Egress.ToProto(),
		Dhcp:         v.GetDhcp(ctx),
	}

	return req
}

func (v *VpcResourceModel) ToUpdateRequest(ctx context.Context) *networkingv1beta1.UpdateVPCRequest {
	req := networkingv1beta1.UpdateVPCRequest{
		Id:          v.Id.ValueString(),
		VpcPrefixes: v.vpcPrefixes(),
		Ingress:     v.Ingress.ToProto(),
		Egress:      v.Egress.ToProto(),
		Dhcp:        v.GetDhcp(ctx),
	}

	return &req
}

func (r *VpcResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_networking_vpc"
}

func (r *VpcResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	hostPrefixTypes := slices.Collect(maps.Keys(networkingv1beta1.HostPrefix_Type_value))
	ipamGatewayAddressPolicies := slices.Collect(maps.Keys(networkingv1beta1.IPAddressManagementPolicy_GatewayAddressPolicy_value))

	resp.Schema = schema.Schema{
		MarkdownDescription: "Create and manage VPCs. Learn more about [CoreWeave VPCs](https://docs.coreweave.com/docs/products/networking/vpc/about-vpcs).",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The unique identifier for the VPC.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the VPC. Must not be longer than 30 characters.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"zone": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The Availability Zone in which the VPC is located.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"vpc_prefixes": schema.SetNestedAttribute{
				Optional:            true,
				MarkdownDescription: "A list of additional prefixes associated with the VPC. For example, CKS clusters use these prefixes for Pod and service CIDR ranges.",
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
			"host_prefix": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "An IPv4 CIDR range used to allocate host addresses when booting compute into a VPC.\nThis CIDR must be have a mask size of /18. If left unspecified, a Zone-specific default value will be applied by the server.\nThis field is immutable once set.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
					stringplanmodifier.UseStateForUnknown(), // required for the resource to work as expected when the value is computed instead of specified
				},
			},
			"host_prefixes": schema.SetNestedAttribute{
				MarkdownDescription: "The IPv4 or IPv6 CIDR ranges used to allocate host addresses when booting compute into a VPC.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
				},
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.RequiresReplaceIfConfigured(),
					setplanmodifier.UseStateForUnknown(), // this comes into play when this is not specified. Instead, we use the state as refreshed for the plan. This has no effect when this is specified.
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "The user-specified name of the host prefix.",
						},
						"type": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: fmt.Sprintf("Controls network connectivity from the prefix to the host. Must be one of: %s.", strings.Join(hostPrefixTypes, ", ")),
							Validators: []validator.String{
								stringvalidator.OneOf(hostPrefixTypes...),
							},
						},
						"prefixes": schema.SetAttribute{
							Required:            true,
							MarkdownDescription: "The VPC-wide aggregates from which host-specific prefixes are allocated. May be IPv4 or IPv6.",
							ElementType:         cidrtypes.IPPrefixType{},
						},
						"ipam": schema.SingleNestedAttribute{
							Required:            true,
							MarkdownDescription: "The configuration for a secondary host prefix.",
							Attributes: map[string]schema.Attribute{
								"prefix_length": schema.Int32Attribute{
									Required:            true,
									MarkdownDescription: "The desired length for each Node's allocation from the VPC-wide aggregate prefix.",
								},
								"gateway_address_policy": schema.StringAttribute{
									Optional:            true,
									Computed:            true,
									MarkdownDescription: fmt.Sprintf("Describes which IP address from the prefix is allocated to the network gateway. Must be one of: %s.", strings.Join(ipamGatewayAddressPolicies, ", ")),
									Default:             stringdefault.StaticString(networkingv1beta1.IPAddressManagementPolicy_UNSPECIFIED.String()),
									Validators: []validator.String{
										stringvalidator.OneOf(ipamGatewayAddressPolicies...),
									},
								},
							},
						},
					},
				},
			},
			"ingress": schema.SingleNestedAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Settings affecting traffic entering the VPC.",
				Attributes: map[string]schema.Attribute{
					"disable_public_services": schema.BoolAttribute{
						Optional:            true,
						MarkdownDescription: "Specifies whether the VPC should prevent public prefixes advertised from Nodes from being imported into public-facing networks, making them inaccessible from the Internet.",
					},
				},
				Default: objectdefault.StaticValue(types.ObjectValueMust(map[string]attr.Type{
					"disable_public_services": types.BoolType,
				}, map[string]attr.Value{
					"disable_public_services": types.BoolValue(false),
				})),
			},
			"egress": schema.SingleNestedAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Settings affecting traffic leaving the VPC.",
				Attributes: map[string]schema.Attribute{
					"disable_public_access": schema.BoolAttribute{
						Optional:            true,
						MarkdownDescription: "Specifies whether the VPC should be blocked from consuming public Internet.",
					},
				},
				Default: objectdefault.StaticValue(types.ObjectValueMust(map[string]attr.Type{
					"disable_public_access": types.BoolType,
				}, map[string]attr.Value{
					"disable_public_access": types.BoolValue(false),
				})),
			},
			"dhcp": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Settings affecting DHCP behavior within the VPC.",
				Attributes: map[string]schema.Attribute{
					"dns": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Settings affecting DNS for DHCP within the VPC",
						Attributes: map[string]schema.Attribute{
							"servers": schema.SetAttribute{
								Optional:            true,
								MarkdownDescription: "The DNS servers to be used by DHCP clients within the VPC.",
								ElementType:         iptypes.IPAddressType{},
							},
						},
					},
				},
			},
		},
	}
}

func (r *VpcResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	// set state once vpc is created
	data.Set(createResp.Msg.Vpc)
	// if we fail to set state, return early as the resource will be orphaned
	if diag := resp.State.Set(ctx, &data); diag.HasError() {
		resp.Diagnostics.Append(diag...)
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
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

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
		if coreweave.IsNotFoundError(err) {
			return
		}
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

// hostPrefixToCtyValue converts a HostPrefixResourceModel to a cty.Value for HCL rendering.
func hostPrefixToCtyValue(hp HostPrefixResourceModel) cty.Value {
	// Convert prefixes slice to cty values
	prefixValues := make([]cty.Value, len(hp.Prefixes))
	for i, p := range hp.Prefixes {
		prefixValues[i] = cty.StringVal(p.ValueString())
	}

	// Build the host prefix object
	hpObj := map[string]cty.Value{
		"name":     cty.StringVal(hp.Name.ValueString()),
		"type":     cty.StringVal(hp.Type.ValueString()),
		"prefixes": cty.SetVal(prefixValues),
	}

	// Add IPAM if present
	if !hp.IPAM.PrefixLength.IsNull() || !hp.IPAM.GatewayAddressPolicy.IsNull() {
		ipamObj := map[string]cty.Value{
			"prefix_length":          cty.NumberIntVal(int64(hp.IPAM.PrefixLength.ValueInt32())),
			"gateway_address_policy": cty.StringVal(hp.IPAM.GatewayAddressPolicy.ValueString()),
		}
		hpObj["ipam"] = cty.ObjectVal(ipamObj)
	}

	return cty.ObjectVal(hpObj)
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

	// Render host_prefixes if present
	if !vpc.HostPrefixes.IsNull() && !vpc.HostPrefixes.IsUnknown() {
		var hostPrefixModels []HostPrefixResourceModel
		vpc.HostPrefixes.ElementsAs(ctx, &hostPrefixModels, false)

		hostPrefixValues := make([]cty.Value, len(hostPrefixModels))
		for i, hp := range hostPrefixModels {
			hostPrefixValues[i] = hostPrefixToCtyValue(hp)
		}

		if len(hostPrefixValues) > 0 {
			resourceBody.SetAttributeValue("host_prefixes", cty.SetVal(hostPrefixValues))
		}
	}

	vpcPrefixes := make([]cty.Value, len(vpc.VpcPrefixes))
	for i, p := range vpc.VpcPrefixes {
		vpcPrefixes[i] = cty.ObjectVal(map[string]cty.Value{
			"name":  cty.StringVal(p.Name.ValueString()),
			"value": cty.StringVal(p.Value.ValueString()),
		})
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
				serverVals := make([]cty.Value, len(servers))
				for i, s := range servers {
					serverVals[i] = cty.StringVal(s.ValueString())
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

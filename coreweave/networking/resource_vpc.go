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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
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

type VpcPrefixResourceModel struct {
	Name                     types.String `tfsdk:"name"`
	Value                    types.String `tfsdk:"value"`
	DisableExternalPropagate types.Bool   `tfsdk:"disable_external_propagate"`
	DisableHostBgpPeering    types.Bool   `tfsdk:"disable_host_bgp_peering"`
	HostDhcpRoute            types.Bool   `tfsdk:"host_dhcp_route"`
	Public                   types.Bool   `tfsdk:"public"`
}

func (v *VpcPrefixResourceModel) ToProto() *networkingv1beta1.Prefix {
	return &networkingv1beta1.Prefix{
		Name:                     v.Name.ValueString(),
		Value:                    v.Value.ValueString(),
		DisableExternalPropagate: v.DisableExternalPropagate.ValueBool(),
		DisableHostBgpPeering:    v.DisableHostBgpPeering.ValueBool(),
		HostDhcpRoute:            v.HostDhcpRoute.ValueBool(),
		Public:                   v.Public.ValueBool(),
	}
}

func (v *VpcPrefixResourceModel) Set(prefix *networkingv1beta1.Prefix) {
	if prefix == nil {
		return
	}

	v.Name = types.StringValue(prefix.Name)
	v.Value = types.StringValue(prefix.Value)
	v.DisableExternalPropagate = types.BoolValue(prefix.DisableExternalPropagate)
	v.DisableHostBgpPeering = types.BoolValue(prefix.DisableHostBgpPeering)
	v.HostDhcpRoute = types.BoolValue(prefix.HostDhcpRoute)
	v.Public = types.BoolValue(prefix.Public)
}

// VpcResourceModel describes the resource data model.
type VpcResourceModel struct {
	Id           types.String             `tfsdk:"id"`
	Zone         types.String             `tfsdk:"zone"`
	Name         types.String             `tfsdk:"name"`
	PubImport    types.Bool               `tfsdk:"pub_import"`
	HostPrefixes types.Set                `tfsdk:"host_prefixes"`
	VpcPrefixes  []VpcPrefixResourceModel `tfsdk:"vpc_prefixes"`
	DnsServers   types.Set                `tfsdk:"dns_servers"`
}

func (v *VpcResourceModel) Set(vpc *networkingv1beta1.VPC) {
	if vpc == nil {
		return
	}

	v.Id = types.StringValue(vpc.Id)
	v.Name = types.StringValue(vpc.Name)
	v.Zone = types.StringValue(vpc.Zone)
	v.PubImport = types.BoolValue(vpc.PubImport)

	if len(vpc.HostPrefixes) > 0 {
		hostPrefixes := []attr.Value{}
		for _, p := range vpc.HostPrefixes {
			hostPrefixes = append(hostPrefixes, types.StringValue(p))
		}
		hp := types.SetValueMust(types.StringType, hostPrefixes)
		v.HostPrefixes = hp
	} else {
		v.HostPrefixes = types.SetNull(types.StringType)
	}

	if len(vpc.VpcPrefixes) > 0 {
		vpcPrefixes := []VpcPrefixResourceModel{}
		for _, p := range vpc.VpcPrefixes {
			vp := VpcPrefixResourceModel{}
			vp.Set(p)
			vpcPrefixes = append(vpcPrefixes, vp)
		}
		v.VpcPrefixes = vpcPrefixes
	} else {
		v.VpcPrefixes = nil
	}

	if len(vpc.DnsServers) > 0 {
		dnsServers := []attr.Value{}
		for _, s := range vpc.DnsServers {
			dnsServers = append(dnsServers, types.StringValue(s))
		}
		ds := types.SetValueMust(types.StringType, dnsServers)
		v.DnsServers = ds
	} else {
		v.DnsServers = types.SetNull(types.StringType)
	}
}

func (v *VpcResourceModel) GetHostPrefixes(ctx context.Context) []string {
	hp := []string{}
	if v.HostPrefixes.IsNull() {
		return hp
	}

	v.HostPrefixes.ElementsAs(ctx, &hp, true)
	return hp
}

func (v *VpcResourceModel) GetDnsServers(ctx context.Context) []string {
	ds := []string{}
	if v.DnsServers.IsNull() {
		return ds
	}

	v.DnsServers.ElementsAs(ctx, &ds, true)
	return ds
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
		Name:         v.Name.ValueString(),
		Zone:         v.Zone.ValueString(),
		PubImport:    v.PubImport.ValueBool(),
		HostPrefixes: v.GetHostPrefixes(ctx),
		VpcPrefixes:  v.vpcPrefixes(),
		DnsServers:   v.GetDnsServers(ctx),
	}
	return req
}

func (v *VpcResourceModel) ToUpdateRequest(ctx context.Context) *networkingv1beta1.UpdateVPCRequest {
	req := networkingv1beta1.UpdateVPCRequest{
		Id:           v.Id.ValueString(),
		PubImport:    v.PubImport.ValueBool(),
		HostPrefixes: v.GetHostPrefixes(ctx),
		VpcPrefixes:  v.vpcPrefixes(),
		DnsServers:   v.GetDnsServers(ctx),
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
			"pub_import": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"host_prefixes": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.RequiresReplaceIf(func(ctx context.Context, req planmodifier.SetRequest, resp *setplanmodifier.RequiresReplaceIfFuncResponse) {
						// Skip if there's no prior state or if the config is unknown
						if req.StateValue.IsNull() || req.PlanValue.IsUnknown() || req.ConfigValue.IsUnknown() {
							return
						}

						prior := []types.String{}
						planned := []types.String{}

						if diag := req.StateValue.ElementsAs(ctx, &prior, false); diag.HasError() {
							resp.Diagnostics = diag
							return
						}

						if diag := req.PlanValue.ElementsAs(ctx, &planned, false); diag.HasError() {
							resp.Diagnostics = diag
							return
						}

						priorSet := map[string]struct{}{}
						for _, p := range prior {
							priorSet[p.ValueString()] = struct{}{}
						}

						plannedSet := map[string]struct{}{}
						for _, p := range planned {
							plannedSet[p.ValueString()] = struct{}{}
						}

						for key := range priorSet {
							if _, ok := plannedSet[key]; !ok {
								resp.Diagnostics.AddWarning("host_prefixes is append-only, removing an existing value will force replacement", fmt.Sprintf("cannot remove existing prefix '%s'", key))
							}
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
						"disable_external_propagate": schema.BoolAttribute{
							Optional: true,
							Computed: true,
							Default:  booldefault.StaticBool(false),
						},
						"disable_host_bgp_peering": schema.BoolAttribute{
							Optional: true,
							Computed: true,
							Default:  booldefault.StaticBool(false),
						},
						"host_dhcp_route": schema.BoolAttribute{
							Optional: true,
							Computed: true,
							Default:  booldefault.StaticBool(false),
						},
						"public": schema.BoolAttribute{
							Optional: true,
							Computed: true,
							Default:  booldefault.StaticBool(false),
						},
					},
				},
			},
			"dns_servers": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
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
	resourceBody.SetAttributeValue("pub_import", cty.BoolVal(vpc.PubImport.ValueBool()))

	hostPrefixes := []cty.Value{}
	for _, p := range vpc.GetHostPrefixes(ctx) {
		hostPrefixes = append(hostPrefixes, cty.StringVal(p))
	}

	if len(hostPrefixes) > 0 {
		resourceBody.SetAttributeValue("host_prefixes", cty.SetVal(hostPrefixes))
	}

	vpcPrefixes := []cty.Value{}
	for _, p := range vpc.VpcPrefixes {
		vpcPrefixes = append(vpcPrefixes, cty.ObjectVal(map[string]cty.Value{
			"name":                       cty.StringVal(p.Name.ValueString()),
			"value":                      cty.StringVal(p.Value.ValueString()),
			"disable_external_propagate": cty.BoolVal(p.DisableExternalPropagate.ValueBool()),
			"disable_host_bgp_peering":   cty.BoolVal(p.DisableHostBgpPeering.ValueBool()),
			"host_dhcp_route":            cty.BoolVal(p.HostDhcpRoute.ValueBool()),
			"public":                     cty.BoolVal(p.Public.ValueBool()),
		}))
	}

	if len(vpcPrefixes) > 0 {
		resourceBody.SetAttributeValue("vpc_prefixes", cty.ListVal(vpcPrefixes))
	}

	dnsServers := []cty.Value{}
	for _, s := range vpc.GetDnsServers(ctx) {
		dnsServers = append(dnsServers, cty.StringVal(s))
	}

	if len(dnsServers) > 0 {
		resourceBody.SetAttributeValue("dns_servers", cty.SetVal(dnsServers))
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

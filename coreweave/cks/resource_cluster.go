package cks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/hcl/v2/hclsyntax"
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
	_                        resource.Resource                = &ClusterResource{}
	_                        resource.ResourceWithImportState = &ClusterResource{}
	errClusterCreationFailed error                            = errors.New("cluster creation failed")
)

func NewClusterResource() resource.Resource {
	return &ClusterResource{}
}

// ClusterResource defines the resource implementation.
type ClusterResource struct {
	client *coreweave.Client
}

type AuthWebhookResourceModel struct {
	Server types.String `tfsdk:"server"`
	CA     types.String `tfsdk:"ca"`
}

type OidcResourceModel struct {
	IssuerURL      types.String `tfsdk:"issuer_url"`
	ClientID       types.String `tfsdk:"client_id"`
	UsernameClaim  types.String `tfsdk:"username_claim"`
	UsernamePrefix types.String `tfsdk:"username_prefix"`
	GroupsClaim    types.String `tfsdk:"groups_claim"`
	GroupsPrefix   types.String `tfsdk:"groups_prefix"`
	CA             types.String `tfsdk:"ca"`
	RequiredClaim  types.String `tfsdk:"required_claim"`
	SigningAlgs    types.Set    `tfsdk:"signing_algs"`
}

func (o *OidcResourceModel) Set(plan *ClusterResourceModel, oidc *cksv1beta1.OIDCConfig) {
	if oidc == nil {
		return
	}

	oidcPlan := plan.Oidc
	if oidcPlan == nil {
		// prevent panics for imports
		oidcPlan = &OidcResourceModel{}
	}

	o.IssuerURL = types.StringValue(oidc.IssuerUrl)
	o.ClientID = types.StringValue(oidc.ClientId)
	o.UsernameClaim = types.StringValue(oidc.UsernameClaim)
	o.UsernamePrefix = types.StringValue(oidc.UsernamePrefix)
	o.GroupsClaim = types.StringValue(oidc.GroupsClaim)
	o.GroupsPrefix = types.StringValue(oidc.GroupsPrefix)
	o.CA = types.StringValue(oidc.Ca)
	o.RequiredClaim = types.StringValue(oidc.RequiredClaim)

	// if we don't have any saved state for these fields, and the API returns empty
	// set these fields to null so as to match unset HCL
	if oidcPlan.UsernameClaim.IsNull() && oidc.UsernameClaim == "" {
		o.UsernameClaim = types.StringNull()
	}

	if oidcPlan.UsernamePrefix.IsNull() && oidc.UsernamePrefix == "" {
		o.UsernamePrefix = types.StringNull()
	}

	if oidcPlan.GroupsClaim.IsNull() && oidc.GroupsClaim == "" {
		o.GroupsClaim = types.StringNull()
	}

	if oidcPlan.GroupsPrefix.IsNull() && oidc.GroupsPrefix == "" {
		o.GroupsPrefix = types.StringNull()
	}

	if oidcPlan.CA.IsNull() && oidc.Ca == "" {
		o.CA = types.StringNull()
	}

	if oidcPlan.RequiredClaim.IsNull() && oidc.RequiredClaim == "" {
		o.RequiredClaim = types.StringNull()
	}

	if oidcPlan.SigningAlgs.IsNull() && len(oidc.SigningAlgorithms) == 0 {
		o.SigningAlgs = types.SetNull(types.StringType)
	} else {
		algs := []attr.Value{}
		for _, a := range oidc.SigningAlgorithms {
			algs = append(algs, types.StringValue(a.String()))
		}
		signingAlgs := types.SetValueMust(types.StringType, algs)
		o.SigningAlgs = signingAlgs
	}
}

// ClusterResourceModel describes the resource data model.
type ClusterResourceModel struct {
	Id                          types.String              `tfsdk:"id"`     //nolint:staticcheck
	VpcId                       types.String              `tfsdk:"vpc_id"` //nolint:staticcheck
	Zone                        types.String              `tfsdk:"zone"`
	Name                        types.String              `tfsdk:"name"`
	Version                     types.String              `tfsdk:"version"`
	Public                      types.Bool                `tfsdk:"public"`
	PodCidrName                 types.String              `tfsdk:"pod_cidr_name"`
	ServiceCidrName             types.String              `tfsdk:"service_cidr_name"`
	InternalLBCidrNames         types.Set                 `tfsdk:"internal_lb_cidr_names"`
	AuditPolicy                 types.String              `tfsdk:"audit_policy"`
	Oidc                        *OidcResourceModel        `tfsdk:"oidc"`
	AuthNWebhook                *AuthWebhookResourceModel `tfsdk:"authn_webhook"`
	AuthZWebhook                *AuthWebhookResourceModel `tfsdk:"authz_webhook"`
	ApiServerEndpoint           types.String              `tfsdk:"api_server_endpoint"` //nolint:staticcheck
	Status                      types.String              `tfsdk:"status"`
	ServiceAccountOIDCIssuerURL types.String              `tfsdk:"service_account_oidc_issuer_url"`
}

func oidcIsEmpty(oidc *cksv1beta1.OIDCConfig) bool {
	if oidc == nil {
		return true
	}

	return oidc.Ca == "" &&
		oidc.ClientId == "" &&
		oidc.GroupsClaim == "" &&
		oidc.GroupsPrefix == "" &&
		oidc.IssuerUrl == "" &&
		oidc.RequiredClaim == "" &&
		len(oidc.SigningAlgorithms) == 0 &&
		oidc.UsernameClaim == "" &&
		oidc.UsernamePrefix == ""
}

func authWebhookEmpty(webhook *cksv1beta1.AuthWebhookConfig) bool {
	if webhook == nil {
		return true
	}

	return webhook.Server == "" && webhook.Ca == ""
}

func (c *ClusterResourceModel) Set(cluster *cksv1beta1.Cluster) {
	if cluster == nil {
		return
	}

	c.Id = types.StringValue(cluster.Id)
	c.VpcId = types.StringValue(cluster.VpcId)
	c.Zone = types.StringValue(cluster.Zone)
	c.Name = types.StringValue(cluster.Name)
	c.Version = types.StringValue(cluster.Version)
	c.Public = types.BoolValue(cluster.Public)
	c.Status = types.StringValue(cluster.Status.String())
	c.ServiceAccountOIDCIssuerURL = types.StringValue(fmt.Sprintf("https://oidc.cks.coreweave.com/id/%s", cluster.Id))

	// if the plan value is null & the API returns an empty string, do not write to state
	if cluster.AuditPolicy == "" && c.AuditPolicy.IsNull() {
		c.AuditPolicy = types.StringNull()
	} else {
		c.AuditPolicy = types.StringValue(cluster.AuditPolicy)
	}

	if cluster.Network != nil {
		c.PodCidrName = types.StringValue(cluster.Network.PodCidrName)
		c.ServiceCidrName = types.StringValue(cluster.Network.ServiceCidrName)
		internalLbCidrs := []attr.Value{}
		for _, c := range cluster.Network.InternalLbCidrNames {
			internalLbCidrs = append(internalLbCidrs, types.StringValue(c))
		}
		c.InternalLBCidrNames = types.SetValueMust(types.StringType, internalLbCidrs)
	}

	if !oidcIsEmpty(cluster.Oidc) {
		oidc := OidcResourceModel{}
		oidc.Set(c, cluster.Oidc)
		c.Oidc = &oidc
	} else {
		c.Oidc = nil
	}

	if !authWebhookEmpty(cluster.AuthnWebhook) && c.AuthNWebhook != nil {
		authnWebhook := &AuthWebhookResourceModel{
			Server: types.StringValue(cluster.AuthnWebhook.Server),
			CA:     types.StringValue(cluster.AuthnWebhook.Ca),
		}

		// if the plan value is null & the API is empty, do not store an empty string
		if c.AuthNWebhook.CA.IsNull() && cluster.AuthnWebhook.Ca == "" {
			authnWebhook.CA = types.StringNull()
		}

		c.AuthNWebhook = authnWebhook
	} else {
		c.AuthNWebhook = nil
	}

	if !authWebhookEmpty(cluster.AuthzWebhook) && c.AuthZWebhook != nil {
		authzWebhook := &AuthWebhookResourceModel{
			Server: types.StringValue(cluster.AuthzWebhook.Server),
			CA:     types.StringValue(cluster.AuthzWebhook.Ca),
		}

		// if the plan value is null & the API is empty, do not store an empty string
		if c.AuthZWebhook.CA.IsNull() && cluster.AuthzWebhook.Ca == "" {
			authzWebhook.CA = types.StringNull()
		}
		c.AuthZWebhook = authzWebhook
	} else {
		c.AuthZWebhook = nil
	}

	c.ApiServerEndpoint = types.StringValue(cluster.ApiServerEndpoint)
}

func (c *ClusterResourceModel) oidcSigningAlgs(ctx context.Context) []cksv1beta1.SigningAlgorithm {
	algs := []types.String{}
	c.Oidc.SigningAlgs.ElementsAs(ctx, &algs, false)

	result := []cksv1beta1.SigningAlgorithm{}
	for _, a := range algs {
		switch a.ValueString() { //nolint:gocritic
		case cksv1beta1.SigningAlgorithm_SIGNING_ALGORITHM_RS256.String():
			result = append(result, cksv1beta1.SigningAlgorithm_SIGNING_ALGORITHM_RS256)
		}
	}

	return result
}

func (c *ClusterResourceModel) InternalLbCidrNames(ctx context.Context) []string {
	lbs := []string{}
	if c.InternalLBCidrNames.IsNull() {
		return lbs
	}

	c.InternalLBCidrNames.ElementsAs(ctx, &lbs, true)
	return lbs
}

func (c *ClusterResourceModel) ToCreateRequest(ctx context.Context) *cksv1beta1.CreateClusterRequest {
	req := &cksv1beta1.CreateClusterRequest{
		Name:    c.Name.ValueString(),
		Zone:    c.Zone.ValueString(),
		VpcId:   c.VpcId.ValueString(),
		Public:  c.Public.ValueBool(),
		Version: c.Version.ValueString(),
		Network: &cksv1beta1.ClusterNetworkConfig{
			PodCidrName:         c.PodCidrName.ValueString(),
			ServiceCidrName:     c.ServiceCidrName.ValueString(),
			InternalLbCidrNames: c.InternalLbCidrNames(ctx),
		},
		AuditPolicy: c.AuditPolicy.ValueString(),
	}

	if c.AuthNWebhook != nil {
		req.AuthnWebhook = &cksv1beta1.AuthWebhookConfig{
			Server: c.AuthNWebhook.Server.ValueString(),
			Ca:     c.AuthNWebhook.CA.ValueString(),
		}
	}

	if c.AuthZWebhook != nil {
		req.AuthzWebhook = &cksv1beta1.AuthWebhookConfig{
			Server: c.AuthZWebhook.Server.ValueString(),
			Ca:     c.AuthZWebhook.CA.ValueString(),
		}
	}

	if c.Oidc != nil {
		req.Oidc = &cksv1beta1.OIDCConfig{
			IssuerUrl:         c.Oidc.IssuerURL.ValueString(),
			ClientId:          c.Oidc.ClientID.ValueString(),
			UsernameClaim:     c.Oidc.UsernameClaim.ValueString(),
			UsernamePrefix:    c.Oidc.UsernamePrefix.ValueString(),
			GroupsClaim:       c.Oidc.GroupsClaim.ValueString(),
			GroupsPrefix:      c.Oidc.GroupsPrefix.ValueString(),
			Ca:                c.Oidc.CA.ValueString(),
			RequiredClaim:     c.Oidc.RequiredClaim.ValueString(),
			SigningAlgorithms: c.oidcSigningAlgs(ctx),
		}
	}

	return req
}

func (c *ClusterResourceModel) ToUpdateRequest(ctx context.Context) *cksv1beta1.UpdateClusterRequest {
	req := cksv1beta1.UpdateClusterRequest{
		Id:          c.Id.ValueString(),
		Public:      c.Public.ValueBool(),
		Version:     c.Version.ValueString(),
		AuditPolicy: c.AuditPolicy.ValueString(),
		Network: &cksv1beta1.UpdateClusterRequest_Network{
			InternalLbCidrNames: c.InternalLbCidrNames(ctx),
		},
	}

	if c.AuthNWebhook != nil {
		req.AuthnWebhook = &cksv1beta1.AuthWebhookConfig{
			Server: c.AuthNWebhook.Server.ValueString(),
			Ca:     c.AuthNWebhook.CA.ValueString(),
		}
	}

	if c.AuthZWebhook != nil {
		req.AuthzWebhook = &cksv1beta1.AuthWebhookConfig{
			Server: c.AuthZWebhook.Server.ValueString(),
			Ca:     c.AuthZWebhook.CA.ValueString(),
		}
	}

	if c.Oidc != nil {
		req.Oidc = &cksv1beta1.OIDCConfig{
			IssuerUrl:         c.Oidc.IssuerURL.ValueString(),
			ClientId:          c.Oidc.ClientID.ValueString(),
			UsernameClaim:     c.Oidc.UsernameClaim.ValueString(),
			UsernamePrefix:    c.Oidc.UsernamePrefix.ValueString(),
			GroupsClaim:       c.Oidc.GroupsClaim.ValueString(),
			GroupsPrefix:      c.Oidc.GroupsPrefix.ValueString(),
			Ca:                c.Oidc.CA.ValueString(),
			RequiredClaim:     c.Oidc.RequiredClaim.ValueString(),
			SigningAlgorithms: c.oidcSigningAlgs(ctx),
		}
	}

	return &req
}

func (r *ClusterResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cks_cluster"
}

func (r *ClusterResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Kubernetes Cluster",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The unique identifier of the cluster.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the cluster. Must not be longer than 30 characters.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"zone": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The Availability Zone in which the cluster is located.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"vpc_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the VPC in which the cluster is located. Must be a VPC in the same Availability Zone as the cluster.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"public": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Whether the cluster's api-server is publicly accessible from the internet.",
				Default:             booldefault.StaticBool(false),
			},
			"version": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The version of Kubernetes to run on the cluster, in minor version format (e.g. 'v1.32'). Patch versions are automatically applied by CKS as they are released.",
			},
			"pod_cidr_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the vpc prefix to use as the pod CIDR range. The prefix must exist in the cluster's VPC.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"service_cidr_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the vpc prefix to use as the service CIDR range. The prefix must exist in the cluster's VPC.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"internal_lb_cidr_names": schema.SetAttribute{
				ElementType:         types.StringType,
				Required:            true,
				MarkdownDescription: "The names of the vpc prefixes to use as internal load balancer CIDR ranges. Internal load balancers are reachable within the VPC but not accessible from the internet.\nThe prefixes must exist in the cluster's VPC. This field is append-only.",
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
								resp.Diagnostics.AddWarning("internal_lb_cidr_names is append-only, removing an existing value will force replacement", fmt.Sprintf("cannot remove existing prefix '%s'", key))
							}
						}

						if resp.Diagnostics.WarningsCount() > 0 {
							resp.RequiresReplace = true
						}
					}, "", "Field `internal_lb_cidr_names` is append-only. Removing an existing value will force replacement."),
				},
			},
			"audit_policy": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Audit policy for the cluster. Must be provided as a base64-encoded JSON/YAML string.",
			},
			"authn_webhook": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Authentication webhook configuration for the cluster.",
				Attributes: map[string]schema.Attribute{
					"server": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The URL of the webhook server.",
					},
					"ca": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The CA certificate for the webhook server. Must be a base64-encoded PEM-encoded certificate.",
					},
				},
			},
			"authz_webhook": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Authorization webhook configuration for the cluster.",
				Attributes: map[string]schema.Attribute{
					"server": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The URL of the webhook server.",
					},
					"ca": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The CA certificate for the webhook server. Must be a base64-encoded PEM-encoded certificate.",
					},
				},
			},
			"oidc": schema.SingleNestedAttribute{
				MarkdownDescription: "OpenID Connect (OIDC) configuration for authentication to the api-server.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"issuer_url": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The URL of the OIDC issuer.",
					},
					"client_id": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The client ID for the OIDC client.",
					},
					"username_claim": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The claim to use as the username.",
					},
					"username_prefix": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The prefix to use for the username.",
					},
					"groups_claim": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The claim to use as the groups.",
					},
					"groups_prefix": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The prefix to use for the groups.",
					},
					"ca": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The CA certificate for the OIDC issuer. Must be a base64-encoded PEM-encoded certificate.",
					},
					"required_claim": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The claim to require for authentication.",
					},
					"signing_algs": schema.SetAttribute{
						ElementType:         types.StringType,
						Optional:            true,
						MarkdownDescription: "A list of signing algorithms that the OpenID Connect discovery endpoint uses.",
					},
				},
			},
			"api_server_endpoint": schema.StringAttribute{
				MarkdownDescription: "The endpoint for the cluster's api-server.",
				Computed:            true,
				Optional:            false,
				Required:            false,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "The current status of the cluster.",
				Computed:            true,
				Optional:            false,
				Required:            false,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplaceIf(
					func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						// Skip if there's no prior state or if the config is unknown
						if req.StateValue.IsNull() || req.PlanValue.IsUnknown() || req.ConfigValue.IsUnknown() {
							return
						}

						// If the status is FAILED, the cluster must be destroyed and re-created
						if req.StateValue.ValueString() == cksv1beta1.Cluster_STATUS_FAILED.String() || req.PlanValue.ValueString() == cksv1beta1.Cluster_STATUS_FAILED.String() {
							resp.Diagnostics.AddWarning("Failed cluster must be destroyed and re-created", "The cluster is in a failed state. You must destroy and re-create the cluster to retry.")
							resp.RequiresReplace = true
						}
					},
					"", "Field `status` is read-only. If the status is `FAILED`, the cluster must be destroyed and re-created again.")},
			},
			"service_account_oidc_issuer_url": schema.StringAttribute{
				MarkdownDescription: "The OIDC issuer URL for service accounts in the cluster.",
				Computed:            true,
			},
		},
	}
}

func (r *ClusterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ClusterResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	createResp, err := r.client.CreateCluster(ctx, connect.NewRequest(data.ToCreateRequest(ctx)))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// set state once cluster is created
	data.Set(createResp.Msg.Cluster)
	// if we fail to set state, return early as the resource will be orphaned
	if diag := resp.State.Set(ctx, &data); diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}

	// wait for the cluster to become ready
	conf := retry.StateChangeConf{
		Pending: []string{
			cksv1beta1.Cluster_STATUS_CREATING.String(),
			cksv1beta1.Cluster_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{cksv1beta1.Cluster_STATUS_RUNNING.String()},
		Refresh: func() (result interface{}, state string, err error) {
			resp, err := r.client.GetCluster(ctx, connect.NewRequest(&cksv1beta1.GetClusterRequest{
				Id: createResp.Msg.Cluster.Id,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to fetch cluster resource", map[string]interface{}{
					"error": err,
				})
				return nil, cksv1beta1.Cluster_STATUS_UNSPECIFIED.String(), err
			}

			if resp.Msg.Cluster.Status == cksv1beta1.Cluster_STATUS_FAILED {
				return resp.Msg.Cluster, resp.Msg.Cluster.Status.String(), errClusterCreationFailed
			}

			return resp.Msg.Cluster, resp.Msg.Cluster.Status.String(), nil
		},
		Timeout: 45 * time.Minute,
	}

	rawCluster, err := conf.WaitForStateContext(ctx)
	if err != nil && !errors.Is(err, errClusterCreationFailed) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	cluster, ok := rawCluster.(*cksv1beta1.Cluster)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Create Type",
			"Expected *cksv1beta1.Cluster. Please report this issue to the provider developers.",
		)
		return
	}

	// If the cluster failed to create, we need to save the resource in the state
	// Upon a fresh read, the resource will be marked as tainted and the user will be able to retry the create
	if cluster.Status == cksv1beta1.Cluster_STATUS_FAILED {
		resp.Diagnostics.AddError("Cluster creation failed", "The cluster creation failed. You must delete and recreate this cluster to retry.")
	}

	data.Set(cluster)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ClusterResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	cluster, err := r.client.GetCluster(ctx, connect.NewRequest(&cksv1beta1.GetClusterRequest{
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

	data.Set(cluster.Msg.Cluster)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ClusterResourceModel
	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	updateResp, err := r.client.UpdateCluster(ctx, connect.NewRequest(data.ToUpdateRequest(ctx)))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// wait for the cluster to become ready
	conf := retry.StateChangeConf{
		Pending: []string{
			cksv1beta1.Cluster_STATUS_UPDATING.String(),
			cksv1beta1.Cluster_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{cksv1beta1.Cluster_STATUS_RUNNING.String()},
		Refresh: func() (result interface{}, state string, err error) {
			resp, err := r.client.GetCluster(ctx, connect.NewRequest(&cksv1beta1.GetClusterRequest{
				Id: updateResp.Msg.Cluster.Id,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to fetch cluster resource", map[string]interface{}{
					"error": err.Error(),
				})
				return nil, cksv1beta1.Cluster_STATUS_UNSPECIFIED.String(), err
			}

			return resp.Msg.Cluster, resp.Msg.Cluster.Status.String(), nil
		},
		Timeout: 20 * time.Minute,
	}

	rawCluster, err := conf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	cluster, ok := rawCluster.(*cksv1beta1.Cluster)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Update Type",
			"Expected *cksv1beta1.VPC. Please report this issue to the provider developers.",
		)
		return
	}

	data.Set(cluster)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ClusterResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteResp, err := r.client.DeleteCluster(ctx, connect.NewRequest(&cksv1beta1.DeleteClusterRequest{
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
			cksv1beta1.Cluster_STATUS_DELETING.String(),
			cksv1beta1.Cluster_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{cksv1beta1.Cluster_STATUS_DELETED.String()},
		Refresh: func() (result interface{}, state string, err error) {
			resp, err := r.client.GetCluster(ctx, connect.NewRequest(&cksv1beta1.GetClusterRequest{
				Id: deleteResp.Msg.Cluster.Id,
			}))
			if err != nil {
				var connectErr *connect.Error
				if errors.As(err, &connectErr) && connectErr.Code() == connect.CodeNotFound {
					return struct{}{}, cksv1beta1.Cluster_STATUS_DELETED.String(), nil
				}

				tflog.Error(ctx, "failed to fetch cluster resource", map[string]interface{}{
					"error": err.Error(),
				})
				return nil, cksv1beta1.Cluster_STATUS_UNSPECIFIED.String(), err
			}

			return resp.Msg.Cluster, resp.Msg.Cluster.Status.String(), nil
		},
		Timeout: 20 * time.Minute,
	}

	_, err = conf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
}

func (r *ClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// MustRenderClusterResource is a helper to render HCL for use in acceptance testing.
// It should not be used by clients of this library.
func MustRenderClusterResource(ctx context.Context, resourceName string, cluster *ClusterResourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{"coreweave_cks_cluster", resourceName})
	resourceBody := resource.Body()

	resourceBody.SetAttributeValue("name", cty.StringVal(cluster.Name.ValueString()))
	resourceBody.SetAttributeValue("zone", cty.StringVal(cluster.Zone.ValueString()))
	resourceBody.SetAttributeRaw("vpc_id", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(cluster.VpcId.ValueString())}})
	resourceBody.SetAttributeValue("version", cty.StringVal(cluster.Version.ValueString()))
	resourceBody.SetAttributeValue("public", cty.BoolVal(cluster.Public.ValueBool()))
	resourceBody.SetAttributeValue("pod_cidr_name", cty.StringVal(cluster.PodCidrName.ValueString()))
	resourceBody.SetAttributeValue("service_cidr_name", cty.StringVal(cluster.ServiceCidrName.ValueString()))
	internalLbCidrs := []types.String{}
	cluster.InternalLBCidrNames.ElementsAs(ctx, &internalLbCidrs, false)
	internalLbCidrSetVals := []cty.Value{}
	for _, lb := range internalLbCidrs {
		internalLbCidrSetVals = append(internalLbCidrSetVals, cty.StringVal(lb.ValueString()))
	}
	resourceBody.SetAttributeValue("internal_lb_cidr_names", cty.SetVal(internalLbCidrSetVals))
	if !cluster.AuditPolicy.IsNull() {
		resourceBody.SetAttributeValue("audit_policy", cty.StringVal(cluster.AuditPolicy.ValueString()))
	}

	stringOrNull := func(s types.String) cty.Value {
		if s.IsNull() || s.IsUnknown() {
			return cty.NullVal(cty.String)
		}

		return cty.StringVal(s.ValueString())
	}

	if cluster.Oidc != nil {
		signingAlgVals := []cty.Value{}
		if !cluster.Oidc.SigningAlgs.IsNull() {
			signingAlgs := []types.String{}
			cluster.Oidc.SigningAlgs.ElementsAs(ctx, &signingAlgs, false)
			for _, s := range signingAlgs {
				signingAlgVals = append(signingAlgVals, cty.StringVal(s.ValueString()))
			}
		}

		var signingAlgs cty.Value
		if len(signingAlgVals) == 0 {
			signingAlgs = cty.SetValEmpty(cty.String)
		} else {
			signingAlgs = cty.SetVal(signingAlgVals)
		}

		resourceBody.SetAttributeValue("oidc", cty.ObjectVal(map[string]cty.Value{
			"issuer_url":      stringOrNull(cluster.Oidc.IssuerURL),
			"client_id":       stringOrNull(cluster.Oidc.ClientID),
			"username_claim":  stringOrNull(cluster.Oidc.UsernameClaim),
			"username_prefix": stringOrNull(cluster.Oidc.UsernamePrefix),
			"groups_claim":    stringOrNull(cluster.Oidc.GroupsClaim),
			"groups_prefix":   stringOrNull(cluster.Oidc.GroupsPrefix),
			"ca":              stringOrNull(cluster.Oidc.CA),
			"required_claim":  stringOrNull(cluster.Oidc.RequiredClaim),
			"signing_algs":    signingAlgs,
		}))
	}

	if cluster.AuthNWebhook != nil {
		resourceBody.SetAttributeValue("authn_webhook", cty.ObjectVal(map[string]cty.Value{
			"server": cty.StringVal(cluster.AuthNWebhook.Server.ValueString()),
			"ca":     stringOrNull(cluster.AuthNWebhook.CA),
		}))
	}

	if cluster.AuthZWebhook != nil {
		resourceBody.SetAttributeValue("authz_webhook", cty.ObjectVal(map[string]cty.Value{
			"server": cty.StringVal(cluster.AuthZWebhook.Server.ValueString()),
			"ca":     stringOrNull(cluster.AuthZWebhook.CA),
		}))
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

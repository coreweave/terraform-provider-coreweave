package cks

import (
	"context"
	"crypto/sha1" //nolint:gosec // CKS public hostnames intentionally use SHA-1.
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	nodePoolAPIPath = "/apis/compute.coreweave.com/v1alpha1/nodepools"
)

var (
	_ resource.Resource                = &NodePoolResource{}
	_ resource.ResourceWithConfigure   = &NodePoolResource{}
	_ resource.ResourceWithImportState = &NodePoolResource{}

	nodePoolNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

func NewNodePoolResource() resource.Resource {
	return &NodePoolResource{}
}

type NodePoolResource struct {
	client *coreweave.Client
}

type NodePoolTaintResourceModel struct {
	Key    types.String `tfsdk:"key"`
	Value  types.String `tfsdk:"value"`
	Effect types.String `tfsdk:"effect"`
}

type NodePoolResourceModel struct {
	ID             types.String                 `tfsdk:"id"`
	ClusterID      types.String                 `tfsdk:"cluster_id"`
	Name           types.String                 `tfsdk:"name"`
	InstanceType   types.String                 `tfsdk:"instance_type"`
	TargetNodes    types.Int64                  `tfsdk:"target_nodes"`
	ComputeClass   types.String                 `tfsdk:"compute_class"`
	Autoscaling    types.Bool                   `tfsdk:"autoscaling"`
	MinNodes       types.Int64                  `tfsdk:"min_nodes"`
	MaxNodes       types.Int64                  `tfsdk:"max_nodes"`
	Labels         types.Map                    `tfsdk:"labels"`
	Annotations    types.Map                    `tfsdk:"annotations"`
	Taints         []NodePoolTaintResourceModel `tfsdk:"taints"`
	UpdateStrategy types.String                 `tfsdk:"update_strategy"`
}

type nodePoolManifest struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   nodePoolMetadata `json:"metadata"`
	Spec       nodePoolSpec     `json:"spec"`
}

type nodePoolMetadata struct {
	Name string `json:"name"`
}

type nodePoolSpec struct {
	InstanceType                    string                               `json:"instanceType"`
	TargetNodes                     int64                                `json:"targetNodes"`
	ComputeClass                    string                               `json:"computeClass,omitempty"`
	Autoscaling                     bool                                 `json:"autoscaling,omitempty"`
	MinNodes                        *int64                               `json:"minNodes,omitempty"`
	MaxNodes                        *int64                               `json:"maxNodes,omitempty"`
	NodeLabels                      map[string]string                    `json:"nodeLabels,omitempty"`
	NodeAnnotations                 map[string]string                    `json:"nodeAnnotations,omitempty"`
	NodeTaints                      []nodePoolTaint                      `json:"nodeTaints,omitempty"`
	NodeConfigurationUpdateStrategy *nodePoolConfigurationUpdateStrategy `json:"nodeConfigurationUpdateStrategy,omitempty"`
}

type nodePoolTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

type nodePoolConfigurationUpdateStrategy struct {
	Type string `json:"type"`
}

func (r *NodePoolResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cks_node_pool"
}

func (r *NodePoolResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Node Pool in a CoreWeave Kubernetes Service (CKS) cluster. The configured provider token must have access to the cluster's Kubernetes API.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier in `<cluster_id>/<name>` form.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cluster_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the CKS cluster that owns the Node Pool.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Kubernetes name of the Node Pool.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 63),
					stringvalidator.RegexMatches(nodePoolNamePattern, "must be a valid DNS label"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"instance_type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "CoreWeave instance type provisioned by the Node Pool.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"target_nodes": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "Desired number of Nodes in the Node Pool.",
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
			},
			"compute_class": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("default"),
				MarkdownDescription: "Immutable compute class for the Node Pool. `spot` Nodes are preemptible.",
				Validators:          []validator.String{stringvalidator.OneOf("default", "spot")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"autoscaling": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether cluster autoscaling is enabled.",
			},
			"min_nodes": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Minimum target node count allowed by the autoscaler.",
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
			},
			"max_nodes": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Maximum target node count allowed by the autoscaler.",
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
			},
			"labels": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Kubernetes labels applied to every Node in the Node Pool.",
			},
			"annotations": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Kubernetes annotations applied to every Node in the Node Pool.",
			},
			"taints": schema.SetNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Kubernetes taints applied to every Node in the Node Pool.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"key": schema.StringAttribute{
						Required:   true,
						Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"value": schema.StringAttribute{
						Optional: true,
						Computed: true,
						Default:  stringdefault.StaticString(""),
					},
					"effect": schema.StringAttribute{
						Required:   true,
						Validators: []validator.String{stringvalidator.OneOf("NoSchedule", "PreferNoSchedule", "NoExecute")},
					},
				}},
			},
			"update_strategy": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("OnSpecUpdate"),
				MarkdownDescription: "How new Node configurations are staged and rolled out.",
				Validators:          []validator.String{stringvalidator.OneOf("Manual", "OnSpecUpdate", "Always", "RolloutOnCommand")},
			},
		},
	}
}

func (r *NodePoolResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *coreweave.Client, got %T", req.ProviderData))
		return
	}
	r.client = client
}

func (r *NodePoolResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data NodePoolResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint, err := r.clusterEndpoint(ctx, data.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Resolve CKS Cluster", err.Error())
		return
	}
	manifest, err := data.manifest(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Build Node Pool", err.Error())
		return
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Encode Node Pool", err.Error())
		return
	}
	result, err := r.client.DoKubernetesRequest(ctx, http.MethodPost, endpoint, nodePoolAPIPath, "application/json", payload)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Create Node Pool", err.Error())
		return
	}
	if err := data.setFromJSON(ctx, result); err != nil {
		resp.Diagnostics.AddError("Unable to Read Created Node Pool", err.Error())
		return
	}
	data.ID = types.StringValue(nodePoolID(data.ClusterID.ValueString(), data.Name.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *NodePoolResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data NodePoolResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	endpoint, err := r.clusterEndpoint(ctx, data.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Resolve CKS Cluster", err.Error())
		return
	}
	result, err := r.client.DoKubernetesRequest(ctx, http.MethodGet, endpoint, nodePoolPath(data.Name.ValueString()), "", nil)
	if coreweave.IsKubernetesNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Node Pool", err.Error())
		return
	}
	if err := data.setFromJSON(ctx, result); err != nil {
		resp.Diagnostics.AddError("Unable to Decode Node Pool", err.Error())
		return
	}
	data.ID = types.StringValue(nodePoolID(data.ClusterID.ValueString(), data.Name.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *NodePoolResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data NodePoolResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	endpoint, err := r.clusterEndpoint(ctx, data.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Resolve CKS Cluster", err.Error())
		return
	}
	spec, err := data.patchSpec(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Build Node Pool Update", err.Error())
		return
	}
	payload, err := json.Marshal(map[string]any{"spec": spec})
	if err != nil {
		resp.Diagnostics.AddError("Unable to Encode Node Pool Update", err.Error())
		return
	}
	result, err := r.client.DoKubernetesRequest(ctx, http.MethodPatch, endpoint, nodePoolPath(data.Name.ValueString()), "application/merge-patch+json", payload)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Update Node Pool", err.Error())
		return
	}
	if err := data.setFromJSON(ctx, result); err != nil {
		resp.Diagnostics.AddError("Unable to Read Updated Node Pool", err.Error())
		return
	}
	data.ID = types.StringValue(nodePoolID(data.ClusterID.ValueString(), data.Name.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *NodePoolResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data NodePoolResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	endpoint, err := r.clusterEndpoint(ctx, data.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Resolve CKS Cluster", err.Error())
		return
	}
	_, err = r.client.DoKubernetesRequest(ctx, http.MethodDelete, endpoint, nodePoolPath(data.Name.ValueString()), "", nil)
	if err != nil && !coreweave.IsKubernetesNotFound(err) {
		resp.Diagnostics.AddError("Unable to Delete Node Pool", err.Error())
	}
}

func (r *NodePoolResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	clusterID, name, ok := strings.Cut(req.ID, "/")
	if !ok || clusterID == "" || name == "" || strings.Contains(name, "/") {
		resp.Diagnostics.AddError("Invalid Node Pool Import ID", "Expected an import ID in `<cluster_id>/<name>` form.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cluster_id"), clusterID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}

func (r *NodePoolResource) clusterEndpoint(ctx context.Context, clusterID string) (string, error) {
	response, err := r.client.GetCluster(ctx, connect.NewRequest(&cksv1beta1.GetClusterRequest{Id: clusterID}))
	if err != nil {
		return "", fmt.Errorf("getting cluster %q: %w", clusterID, err)
	}
	cluster := response.Msg.Cluster
	if cluster == nil {
		return "", fmt.Errorf("cluster %q response did not contain a cluster", clusterID)
	}
	if !cluster.Public {
		if cluster.ApiServerEndpoint == "" {
			return "", fmt.Errorf("cluster %q has no reachable API server endpoint", clusterID)
		}
		return withHTTPS(cluster.ApiServerEndpoint), nil
	}
	identity, err := r.client.GetCallerIdentity(ctx)
	if err != nil {
		return "", fmt.Errorf("getting caller identity: %w", err)
	}
	return publicClusterEndpoint(identity.OrganizationID, cluster.Name, cluster.Zone), nil
}

func publicClusterEndpoint(organizationID, clusterName, zone string) string {
	digest := sha1.Sum([]byte(clusterName)) //nolint:gosec // Required by the CKS hostname contract.
	nameHash := hex.EncodeToString(digest[:])[:8]
	return fmt.Sprintf("https://%s-%s.k8s.%s.coreweave.com", strings.ToLower(organizationID), nameHash, strings.ToLower(zone))
}

// ResolveClusterKubernetesEndpoint returns the Kubernetes API endpoint used by
// Node Pool resources for a CKS cluster.
func ResolveClusterKubernetesEndpoint(ctx context.Context, client *coreweave.Client, clusterID string) (string, error) {
	return (&NodePoolResource{client: client}).clusterEndpoint(ctx, clusterID)
}

func withHTTPS(endpoint string) string {
	if strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	return "https://" + endpoint
}

func nodePoolID(clusterID, name string) string {
	return clusterID + "/" + name
}

func nodePoolPath(name string) string {
	return nodePoolAPIPath + "/" + name
}

func (m *NodePoolResourceModel) manifest(ctx context.Context) (nodePoolManifest, error) {
	labels, err := stringMap(ctx, m.Labels)
	if err != nil {
		return nodePoolManifest{}, fmt.Errorf("converting labels: %w", err)
	}
	annotations, err := stringMap(ctx, m.Annotations)
	if err != nil {
		return nodePoolManifest{}, fmt.Errorf("converting annotations: %w", err)
	}
	spec := nodePoolSpec{
		InstanceType:                    m.InstanceType.ValueString(),
		TargetNodes:                     m.TargetNodes.ValueInt64(),
		ComputeClass:                    m.ComputeClass.ValueString(),
		Autoscaling:                     m.Autoscaling.ValueBool(),
		MinNodes:                        int64Pointer(m.MinNodes),
		MaxNodes:                        int64Pointer(m.MaxNodes),
		NodeLabels:                      labels,
		NodeAnnotations:                 annotations,
		NodeTaints:                      taintsFromModel(m.Taints),
		NodeConfigurationUpdateStrategy: &nodePoolConfigurationUpdateStrategy{Type: m.UpdateStrategy.ValueString()},
	}
	return nodePoolManifest{
		APIVersion: "compute.coreweave.com/v1alpha1",
		Kind:       "NodePool",
		Metadata:   nodePoolMetadata{Name: m.Name.ValueString()},
		Spec:       spec,
	}, nil
}

func (m *NodePoolResourceModel) patchSpec(ctx context.Context) (map[string]any, error) {
	manifest, err := m.manifest(ctx)
	if err != nil {
		return nil, err
	}
	spec := map[string]any{
		"targetNodes":                     manifest.Spec.TargetNodes,
		"autoscaling":                     manifest.Spec.Autoscaling,
		"nodeLabels":                      nullableMap(manifest.Spec.NodeLabels, m.Labels.IsNull()),
		"nodeAnnotations":                 nullableMap(manifest.Spec.NodeAnnotations, m.Annotations.IsNull()),
		"nodeTaints":                      nullableTaints(manifest.Spec.NodeTaints, m.Taints == nil),
		"nodeConfigurationUpdateStrategy": manifest.Spec.NodeConfigurationUpdateStrategy,
	}
	if manifest.Spec.MinNodes == nil {
		spec["minNodes"] = nil
	} else {
		spec["minNodes"] = *manifest.Spec.MinNodes
	}
	if manifest.Spec.MaxNodes == nil {
		spec["maxNodes"] = nil
	} else {
		spec["maxNodes"] = *manifest.Spec.MaxNodes
	}
	return spec, nil
}

func (m *NodePoolResourceModel) setFromJSON(ctx context.Context, payload []byte) error {
	var manifest nodePoolManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return err
	}
	m.Name = types.StringValue(manifest.Metadata.Name)
	m.InstanceType = types.StringValue(manifest.Spec.InstanceType)
	m.TargetNodes = types.Int64Value(manifest.Spec.TargetNodes)
	computeClass := manifest.Spec.ComputeClass
	if computeClass == "" {
		computeClass = "default"
	}
	m.ComputeClass = types.StringValue(computeClass)
	m.Autoscaling = types.BoolValue(manifest.Spec.Autoscaling)
	m.MinNodes = int64Value(manifest.Spec.MinNodes)
	m.MaxNodes = int64Value(manifest.Spec.MaxNodes)
	m.UpdateStrategy = types.StringValue("OnSpecUpdate")
	if manifest.Spec.NodeConfigurationUpdateStrategy != nil && manifest.Spec.NodeConfigurationUpdateStrategy.Type != "" {
		m.UpdateStrategy = types.StringValue(manifest.Spec.NodeConfigurationUpdateStrategy.Type)
	}

	var diagsErr error
	m.Labels, diagsErr = mapValue(ctx, manifest.Spec.NodeLabels, m.Labels.IsNull())
	if diagsErr != nil {
		return fmt.Errorf("converting labels: %w", diagsErr)
	}
	m.Annotations, diagsErr = mapValue(ctx, manifest.Spec.NodeAnnotations, m.Annotations.IsNull())
	if diagsErr != nil {
		return fmt.Errorf("converting annotations: %w", diagsErr)
	}
	if manifest.Spec.NodeTaints == nil && m.Taints == nil {
		m.Taints = nil
	} else {
		m.Taints = make([]NodePoolTaintResourceModel, len(manifest.Spec.NodeTaints))
		for i, taint := range manifest.Spec.NodeTaints {
			m.Taints[i] = NodePoolTaintResourceModel{
				Key: types.StringValue(taint.Key), Value: types.StringValue(taint.Value), Effect: types.StringValue(taint.Effect),
			}
		}
	}
	return nil
}

func stringMap(ctx context.Context, value types.Map) (map[string]string, error) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	result := map[string]string{}
	diags := value.ElementsAs(ctx, &result, false)
	if diags.HasError() {
		return nil, fmt.Errorf("%v", diags.Errors())
	}
	return result, nil
}

func mapValue(ctx context.Context, value map[string]string, preserveNull bool) (types.Map, error) {
	if value == nil && preserveNull {
		return types.MapNull(types.StringType), nil
	}
	result, diags := types.MapValueFrom(ctx, types.StringType, value)
	if diags.HasError() {
		return types.MapNull(types.StringType), fmt.Errorf("%v", diags.Errors())
	}
	return result, nil
}

func int64Pointer(value types.Int64) *int64 {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	v := value.ValueInt64()
	return &v
}

func int64Value(value *int64) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*value)
}

func taintsFromModel(values []NodePoolTaintResourceModel) []nodePoolTaint {
	if values == nil {
		return nil
	}
	result := make([]nodePoolTaint, len(values))
	for i, value := range values {
		result[i] = nodePoolTaint{Key: value.Key.ValueString(), Value: value.Value.ValueString(), Effect: value.Effect.ValueString()}
	}
	return result
}

func nullableMap(value map[string]string, isNull bool) any {
	if isNull {
		return nil
	}
	return value
}

func nullableTaints(value []nodePoolTaint, isNull bool) any {
	if isNull {
		return nil
	}
	return value
}

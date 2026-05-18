package sandbox

import (
	"context"
	"fmt"

	sandboxv1beta2 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1beta2"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var (
	_ resource.Resource                = &ProfileTemplateResource{}
	_ resource.ResourceWithImportState = &ProfileTemplateResource{}
	_ resource.ResourceWithConfigure   = &ProfileTemplateResource{}
)

func NewProfileTemplateResource() resource.Resource {
	return &ProfileTemplateResource{}
}

type ProfileTemplateResource struct {
	client *coreweave.Client
}

// ResourceDefaultsModel mirrors coreweave.sandbox.v1beta2.ResourceDefaults.
type ResourceDefaultsModel struct {
	CPURequest    types.String `tfsdk:"cpu_request"`
	MemoryRequest types.String `tfsdk:"memory_request"`
	CPULimit      types.String `tfsdk:"cpu_limit"`
	MemoryLimit   types.String `tfsdk:"memory_limit"`
}

func (r *ResourceDefaultsModel) ToProto() *sandboxv1beta2.ResourceDefaults {
	if r == nil {
		return nil
	}
	return &sandboxv1beta2.ResourceDefaults{
		CpuRequest:    r.CPURequest.ValueString(),
		MemoryRequest: r.MemoryRequest.ValueString(),
		CpuLimit:      r.CPULimit.ValueString(),
		MemoryLimit:   r.MemoryLimit.ValueString(),
	}
}

func resourceDefaultsFromProto(p *sandboxv1beta2.ResourceDefaults) *ResourceDefaultsModel {
	if p == nil {
		return nil
	}
	if p.GetCpuRequest() == "" && p.GetMemoryRequest() == "" && p.GetCpuLimit() == "" && p.GetMemoryLimit() == "" {
		return nil
	}
	return &ResourceDefaultsModel{
		CPURequest:    stringOrNull(p.GetCpuRequest()),
		MemoryRequest: stringOrNull(p.GetMemoryRequest()),
		CPULimit:      stringOrNull(p.GetCpuLimit()),
		MemoryLimit:   stringOrNull(p.GetMemoryLimit()),
	}
}

// ProfileSpecModel mirrors coreweave.sandbox.v1beta2.ProfileSpec.
type ProfileSpecModel struct {
	ContainerImage      types.String           `tfsdk:"container_image"`
	RuntimeClass        types.String           `tfsdk:"runtime_class"`
	ResourceDefaults    *ResourceDefaultsModel `tfsdk:"resource_defaults"`
	InstanceTypes       types.List             `tfsdk:"instance_types"`
	NodeSelector        types.Map              `tfsdk:"node_selector"`
	Tags                types.List             `tfsdk:"tags"`
	NamespaceConfigJSON types.String           `tfsdk:"namespace_config_json"`
	NetworkConfigJSON   types.String           `tfsdk:"network_config_json"`
	PodTemplateJSON     types.String           `tfsdk:"pod_template_json"`
}

func (s *ProfileSpecModel) ToProto(ctx context.Context) (*sandboxv1beta2.ProfileSpec, diag.Diagnostics) {
	if s == nil {
		return nil, nil
	}
	var diags diag.Diagnostics

	instanceTypes, d := stringListToSlice(ctx, s.InstanceTypes)
	diags.Append(d...)

	nodeSelector, d := stringMapToMap(ctx, s.NodeSelector)
	diags.Append(d...)

	tags, d := stringListToSlice(ctx, s.Tags)
	diags.Append(d...)

	if diags.HasError() {
		return nil, diags
	}

	return &sandboxv1beta2.ProfileSpec{
		ContainerImage:      s.ContainerImage.ValueString(),
		RuntimeClass:        s.RuntimeClass.ValueString(),
		ResourceDefaults:    s.ResourceDefaults.ToProto(),
		InstanceTypes:       instanceTypes,
		NodeSelector:        nodeSelector,
		Tags:                tags,
		NamespaceConfigJson: s.NamespaceConfigJSON.ValueString(),
		NetworkConfigJson:   s.NetworkConfigJSON.ValueString(),
		PodTemplateJson:     s.PodTemplateJSON.ValueString(),
	}, nil
}

func profileSpecFromProto(ctx context.Context, prior *ProfileSpecModel, p *sandboxv1beta2.ProfileSpec) (*ProfileSpecModel, diag.Diagnostics) {
	if p == nil {
		return nil, nil
	}
	var diags diag.Diagnostics

	out := &ProfileSpecModel{
		ContainerImage:      stringOrNull(p.GetContainerImage()),
		RuntimeClass:        stringOrNull(p.GetRuntimeClass()),
		ResourceDefaults:    resourceDefaultsFromProto(p.GetResourceDefaults()),
		NamespaceConfigJSON: stringOrNull(p.GetNamespaceConfigJson()),
		NetworkConfigJSON:   stringOrNull(p.GetNetworkConfigJson()),
		PodTemplateJSON:     stringOrNull(p.GetPodTemplateJson()),
	}

	priorInstanceTypes := types.ListNull(types.StringType)
	if prior != nil {
		priorInstanceTypes = prior.InstanceTypes
	}
	instanceTypes, d := stringSliceToList(ctx, p.GetInstanceTypes(), priorInstanceTypes)
	diags.Append(d...)
	out.InstanceTypes = instanceTypes

	priorNodeSelector := types.MapNull(types.StringType)
	if prior != nil {
		priorNodeSelector = prior.NodeSelector
	}
	nodeSelector, d := stringMapFromMap(ctx, p.GetNodeSelector(), priorNodeSelector)
	diags.Append(d...)
	out.NodeSelector = nodeSelector

	priorTags := types.ListNull(types.StringType)
	if prior != nil {
		priorTags = prior.Tags
	}
	tags, d := stringSliceToList(ctx, p.GetTags(), priorTags)
	diags.Append(d...)
	out.Tags = tags

	return out, diags
}

// ProfileTemplateResourceModel describes the resource data model.
type ProfileTemplateResourceModel struct {
	ID             types.String      `tfsdk:"id"`
	OrganizationID types.String      `tfsdk:"organization_id"`
	DisplayName    types.String      `tfsdk:"display_name"`
	Description    types.String      `tfsdk:"description"`
	Spec           *ProfileSpecModel `tfsdk:"spec"`
	Labels         types.Map         `tfsdk:"labels"`
	CreatedAt      types.String      `tfsdk:"created_at"`
	UpdatedAt      types.String      `tfsdk:"updated_at"`
}

func (m *ProfileTemplateResourceModel) Set(ctx context.Context, t *sandboxv1beta2.ProfileTemplate) diag.Diagnostics {
	if t == nil {
		return nil
	}
	var diags diag.Diagnostics

	m.ID = types.StringValue(t.GetId())
	m.OrganizationID = types.StringValue(t.GetOrganizationId())
	m.DisplayName = types.StringValue(t.GetDisplayName())
	m.Description = stringOrNull(t.GetDescription())

	spec, d := profileSpecFromProto(ctx, m.Spec, t.GetSpec())
	diags.Append(d...)
	m.Spec = spec

	priorLabels := types.MapNull(types.StringType)
	if !m.Labels.IsNull() && !m.Labels.IsUnknown() {
		priorLabels = m.Labels
	}
	labels, d := stringMapFromMap(ctx, t.GetLabels(), priorLabels)
	diags.Append(d...)
	m.Labels = labels

	m.CreatedAt = timestampString(t.GetCreatedAt())
	m.UpdatedAt = timestampString(t.GetUpdatedAt())

	return diags
}

func (m *ProfileTemplateResourceModel) ToProto(ctx context.Context) (*sandboxv1beta2.ProfileTemplate, diag.Diagnostics) {
	var diags diag.Diagnostics

	spec, d := m.Spec.ToProto(ctx)
	diags.Append(d...)

	labels, d := stringMapToMap(ctx, m.Labels)
	diags.Append(d...)

	if diags.HasError() {
		return nil, diags
	}

	return &sandboxv1beta2.ProfileTemplate{
		Id:          m.ID.ValueString(),
		DisplayName: m.DisplayName.ValueString(),
		Description: m.Description.ValueString(),
		Spec:        spec,
		Labels:      labels,
	}, nil
}

func (r *ProfileTemplateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sandbox_profile_template"
}

func (r *ProfileTemplateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage CoreWeave Sandbox profile templates. " +
			"A profile template is a reusable specification (image, resources, node placement, networking) that managed runners attach via `profile_bindings`.\n\n" +
			"The `description`, `spec`, and `labels` fields are mutable; changing `display_name` forces replacement.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID for the profile template.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"organization_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Organization that owns this template.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"display_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Human-readable template name. Must be unique within the organization. Changing this forces a replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Human-readable description.",
			},
			"spec": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Profile specification — compute, networking, and placement defaults applied to sandboxes that select this profile.",
				Attributes: map[string]schema.Attribute{
					"container_image": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Container image override (e.g., `ghcr.io/coreweave/sandbox-runtime:v1`).",
					},
					"runtime_class": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Runtime class name (e.g., `kata-qemu`, `gvisor`) used for isolation.",
					},
					"resource_defaults": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Default CPU and memory requests/limits applied to sandboxes that don't specify their own.",
						Attributes: map[string]schema.Attribute{
							"cpu_request":    schema.StringAttribute{Optional: true, MarkdownDescription: "Default CPU request (e.g., `500m`)."},
							"memory_request": schema.StringAttribute{Optional: true, MarkdownDescription: "Default memory request (e.g., `1Gi`)."},
							"cpu_limit":      schema.StringAttribute{Optional: true, MarkdownDescription: "Default CPU limit (e.g., `2`)."},
							"memory_limit":   schema.StringAttribute{Optional: true, MarkdownDescription: "Default memory limit (e.g., `4Gi`)."},
						},
					},
					"instance_types": schema.ListAttribute{
						Optional:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Allowed instance types for sandboxes using this profile.",
					},
					"node_selector": schema.MapAttribute{
						Optional:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Node selector labels.",
					},
					"tags": schema.ListAttribute{
						Optional:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Tags for RBAC-based access control.",
					},
					"namespace_config_json": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Namespace strategy configuration. Must be canonical JSON; use `jsonencode({...})` to construct.",
					},
					"network_config_json": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Network configuration. Must be canonical JSON; use `jsonencode({...})` to construct.",
					},
					"pod_template_json": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Pod template overrides as a JSON-encoded `PodSpec` fragment. Must be canonical JSON; use `jsonencode({...})` to construct.",
					},
				},
			},
			"labels": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "User-defined labels for organizing templates.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp at which the template was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp at which the template was most recently updated.",
			},
		},
	}
}

func (r *ProfileTemplateResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ProfileTemplateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ProfileTemplateResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pt, diags := data.ToProto(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createResp, err := r.client.CreateProfileTemplate(ctx, connect.NewRequest(&sandboxv1beta2.CreateProfileTemplateRequest{
		ProfileTemplate: pt,
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(data.Set(ctx, createResp.Msg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProfileTemplateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ProfileTemplateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := r.client.GetProfileTemplate(ctx, connect.NewRequest(&sandboxv1beta2.GetProfileTemplateRequest{
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

	resp.Diagnostics.Append(data.Set(ctx, getResp.Msg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProfileTemplateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ProfileTemplateResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateReq, diags := buildProfileTemplateUpdateRequest(ctx, &plan, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateResp, err := r.client.UpdateProfileTemplate(ctx, connect.NewRequest(updateReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(plan.Set(ctx, updateResp.Msg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ProfileTemplateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ProfileTemplateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.DeleteProfileTemplate(ctx, connect.NewRequest(&sandboxv1beta2.DeleteProfileTemplateRequest{
		Id: data.ID.ValueString(),
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
	}
}

func (r *ProfileTemplateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// buildProfileTemplateUpdateRequest constructs an UpdateProfileTemplateRequest containing only
// the fields that differ between plan and state, with the corresponding update_mask paths.
// Mutable paths per proto: "description", "spec", "labels".
func buildProfileTemplateUpdateRequest(ctx context.Context, plan, state *ProfileTemplateResourceModel) (*sandboxv1beta2.UpdateProfileTemplateRequest, diag.Diagnostics) {
	var diags diag.Diagnostics
	var paths []string

	out := &sandboxv1beta2.ProfileTemplate{
		Id: plan.ID.ValueString(),
	}

	if !plan.Description.Equal(state.Description) {
		out.Description = plan.Description.ValueString()
		paths = append(paths, "description")
	}
	if !profileSpecEqual(plan.Spec, state.Spec) {
		spec, d := plan.Spec.ToProto(ctx)
		diags.Append(d...)
		out.Spec = spec
		paths = append(paths, "spec")
	}
	if !plan.Labels.Equal(state.Labels) {
		labels, d := stringMapToMap(ctx, plan.Labels)
		diags.Append(d...)
		out.Labels = labels
		paths = append(paths, "labels")
	}

	if diags.HasError() {
		return nil, diags
	}

	return &sandboxv1beta2.UpdateProfileTemplateRequest{
		ProfileTemplate: out,
		UpdateMask:      &fieldmaskpb.FieldMask{Paths: paths},
	}, diags
}

// profileSpecEqual compares two ProfileSpec models for equality of all user-settable fields.
func profileSpecEqual(a, b *ProfileSpecModel) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if !a.ContainerImage.Equal(b.ContainerImage) ||
		!a.RuntimeClass.Equal(b.RuntimeClass) ||
		!a.InstanceTypes.Equal(b.InstanceTypes) ||
		!a.NodeSelector.Equal(b.NodeSelector) ||
		!a.Tags.Equal(b.Tags) ||
		!a.NamespaceConfigJSON.Equal(b.NamespaceConfigJSON) ||
		!a.NetworkConfigJSON.Equal(b.NetworkConfigJSON) ||
		!a.PodTemplateJSON.Equal(b.PodTemplateJSON) {
		return false
	}
	return resourceDefaultsEqual(a.ResourceDefaults, b.ResourceDefaults)
}

func resourceDefaultsEqual(a, b *ResourceDefaultsModel) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.CPURequest.Equal(b.CPURequest) &&
		a.MemoryRequest.Equal(b.MemoryRequest) &&
		a.CPULimit.Equal(b.CPULimit) &&
		a.MemoryLimit.Equal(b.MemoryLimit)
}


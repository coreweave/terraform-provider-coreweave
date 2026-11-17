package serviceaccount

import (
	"context"
	"fmt"
	"regexp"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"connectrpc.com/connect/v2"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var (
	_ resource.Resource                = &AccessPolicyResource{}
	_ resource.ResourceWithConfigure   = &AccessPolicyResource{}
	_ resource.ResourceWithImportState = &AccessPolicyResource{}
)

var accessPolicyRuleAttributeTypes = map[string]attr.Type{
	"actions":          types.SetType{ElemType: types.StringType},
	"users":            types.SetType{ElemType: types.StringType},
	"groups":           types.SetType{ElemType: types.StringType},
	"service_accounts": types.SetType{ElemType: types.StringType},
}

type AccessPolicyResource struct {
	client *coreweave.Client
}

type AccessPolicyResourceModel struct {
	OrganizationID types.String                    `tfsdk:"organization_id"`
	AccessPolicyID types.String                    `tfsdk:"access_policy_id"`
	Name           types.String                    `tfsdk:"name"`
	UID            types.String                    `tfsdk:"uid"`
	Description    types.String                    `tfsdk:"description"`
	PolicyType     types.String                    `tfsdk:"policy_type"`
	Active         types.Bool                      `tfsdk:"active"`
	Rules          []AccessPolicyRuleResourceModel `tfsdk:"rules"`
	CreatedAt      types.String                    `tfsdk:"created_at"`
	UpdatedAt      types.String                    `tfsdk:"updated_at"`
}

type AccessPolicyRuleResourceModel struct {
	Actions         types.Set `tfsdk:"actions"`
	Users           types.Set `tfsdk:"users"`
	Groups          types.Set `tfsdk:"groups"`
	ServiceAccounts types.Set `tfsdk:"service_accounts"`
}

func NewAccessPolicyResource() resource.Resource { return &AccessPolicyResource{} }

func (r *AccessPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_iam_access_policy"
}

func (r *AccessPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	immutable := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computed := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	emptySet := setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{}))
	emptyRules := setdefault.StaticValue(types.SetValueMust(types.ObjectType{AttrTypes: accessPolicyRuleAttributeTypes}, []attr.Value{}))
	principalValidator := func(collection string) []validator.Set {
		identifier := `[^/]+`
		if collection == "serviceAccounts" {
			identifier = `[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?`
		}
		pattern := regexp.MustCompile(fmt.Sprintf(`^organizations/[a-z0-9]{6}/%s/%s$`, collection, identifier))
		return []validator.Set{setvalidator.ValueStringsAre(stringvalidator.RegexMatches(pattern, fmt.Sprintf("must use organizations/{organization}/%s/{id} format", collection)))}
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "> **Private beta:** This resource is available only to organizations enrolled in the private beta and may change before general availability. It is gated by an organization-level feature flag. Contact your CoreWeave representative or CoreWeave Support to request access for your organization.\n\n" +
			"Creates and manages a custom CoreWeave IAM access policy. Rules bind console actions to users, groups, and service accounts.\n\n" +
			"To avoid an interruption in permissions when changing `access_policy_id` or `organization_id`, configure `lifecycle { create_before_destroy = true }`.",
		Attributes: map[string]schema.Attribute{
			"access_policy_id": schema.StringAttribute{
				Required: true, PlanModifiers: immutable,
				Validators:          []validator.String{stringvalidator.RegexMatches(resourceIDPattern, "must start with a lowercase letter, contain only lowercase letters, digits, or hyphens, and be at most 63 characters")},
				MarkdownDescription: "The caller-defined access policy ID used as the final segment of the resource name. It must be unique within the organization.",
			},
			"organization_id": schema.StringAttribute{
				Required: true, PlanModifiers: immutable,
				Validators:          []validator.String{stringvalidator.RegexMatches(organizationIDPattern, "must contain exactly six lowercase letters or digits")},
				MarkdownDescription: "The six-character ID of the CoreWeave organization that owns the policy. It must match the organization authenticated by the provider.",
			},
			"name": schema.StringAttribute{Computed: true, PlanModifiers: computed, MarkdownDescription: "The canonical access policy resource name."},
			"uid":  schema.StringAttribute{Computed: true, PlanModifiers: computed, MarkdownDescription: "The server-generated access policy UUID."},
			"description": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString(""),
				MarkdownDescription: "An optional description of the policy. Defaults to an empty string.",
			},
			"policy_type": schema.StringAttribute{Computed: true, PlanModifiers: computed, MarkdownDescription: "The API policy classification. Terraform-created policies are `POLICY_TYPE_CUSTOM`."},
			"active": schema.BoolAttribute{
				Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "Whether the policy is active.",
			},
			"rules": schema.SetNestedAttribute{
				Optional: true, Computed: true, Default: emptyRules,
				Validators:          []validator.Set{setvalidator.SizeAtMost(25)},
				MarkdownDescription: "Unordered policy rules. Updating this attribute replaces the complete rule set.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"actions": schema.SetAttribute{
						Required: true, ElementType: types.StringType,
						Validators:          []validator.Set{setvalidator.SizeBetween(1, 100)},
						MarkdownDescription: "The console action identifiers granted by this rule.",
					},
					"users": schema.SetAttribute{
						Optional: true, Computed: true, Default: emptySet, ElementType: types.StringType,
						Validators:          append([]validator.Set{setvalidator.SizeAtMost(500)}, principalValidator("users")...),
						MarkdownDescription: "Canonical user resource names assigned directly to this rule.",
					},
					"groups": schema.SetAttribute{
						Optional: true, Computed: true, Default: emptySet, ElementType: types.StringType,
						Validators:          append([]validator.Set{setvalidator.SizeAtMost(250)}, principalValidator("groups")...),
						MarkdownDescription: "Canonical group resource names assigned directly to this rule.",
					},
					"service_accounts": schema.SetAttribute{
						Optional: true, Computed: true, Default: emptySet, ElementType: types.StringType,
						Validators:          append([]validator.Set{setvalidator.SizeAtMost(500)}, principalValidator("serviceAccounts")...),
						MarkdownDescription: "Canonical service account resource names assigned directly to this rule, in `organizations/{organization}/serviceAccounts/{service_account}` format.",
					},
				}},
			},
			"created_at": schema.StringAttribute{Computed: true, PlanModifiers: computed, MarkdownDescription: "The RFC 3339 creation timestamp."},
			"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "The RFC 3339 last-update timestamp, including rule assignment changes."},
		},
	}
}

func (r *AccessPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *coreweave.Client, got %T.", req.ProviderData))
		return
	}
	r.client = client
}

func stringSet(values []string) types.Set {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.SetValueMust(types.StringType, elements)
}

func (m *AccessPolicyResourceModel) setFromProto(policy *directoryv1alpha.AccessPolicy) error {
	if policy == nil {
		return fmt.Errorf("access policy is missing")
	}
	organizationID, accessPolicyID, err := parseAccessPolicyName(policy.GetName())
	if err != nil {
		return err
	}
	if !uuidV4Pattern.MatchString(policy.GetUid()) {
		return fmt.Errorf("access policy UID is missing or is not a UUIDv4")
	}
	if policy.GetOrgId() == "" || policy.GetOrgId() != organizationID {
		return fmt.Errorf("access policy organization ID is missing or does not match its resource name")
	}
	if policy.GetPolicyType() != directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM {
		return fmt.Errorf("only custom access policies can be managed by this Terraform resource, got %s", policy.GetPolicyType())
	}
	m.OrganizationID = types.StringValue(organizationID)
	m.AccessPolicyID = types.StringValue(accessPolicyID)
	m.Name = types.StringValue(policy.GetName())
	m.UID = types.StringValue(policy.GetUid())
	m.Description = types.StringValue(policy.GetDescription())
	m.PolicyType = types.StringValue(policy.GetPolicyType().String())
	m.Active = types.BoolValue(policy.GetActive())
	m.Rules = make([]AccessPolicyRuleResourceModel, 0, len(policy.GetRules()))
	for _, rule := range policy.GetRules() {
		if rule == nil {
			return fmt.Errorf("access policy contains a missing rule")
		}
		m.Rules = append(m.Rules, AccessPolicyRuleResourceModel{
			Actions:         stringSet(rule.GetActions()),
			Users:           stringSet(rule.GetUsers()),
			Groups:          stringSet(rule.GetGroups()),
			ServiceAccounts: stringSet(rule.GetServiceAccounts()),
		})
	}
	m.CreatedAt = timestampValue(policy.GetCreateTime())
	m.UpdatedAt = timestampValue(policy.GetUpdateTime())
	return nil
}

func (m *AccessPolicyResourceModel) fillUnknownFromProto(policy *directoryv1alpha.AccessPolicy) error {
	var remote AccessPolicyResourceModel
	if err := remote.setFromProto(policy); err != nil {
		return err
	}
	m.OrganizationID = remote.OrganizationID
	m.AccessPolicyID = remote.AccessPolicyID
	m.Name = remote.Name
	m.UID = remote.UID
	m.PolicyType = remote.PolicyType
	m.Active = remote.Active
	m.CreatedAt = remote.CreatedAt
	m.UpdatedAt = remote.UpdatedAt
	if m.Description.IsUnknown() {
		m.Description = remote.Description
	}
	if len(m.Rules) != len(remote.Rules) {
		m.Rules = remote.Rules
		return nil
	}
	matched := make([]bool, len(remote.Rules))
	for i := range m.Rules {
		remoteIndex := -1
		for candidateIndex, candidate := range remote.Rules {
			if !matched[candidateIndex] && accessPolicyRuleMatches(m.Rules[i], candidate) {
				remoteIndex = candidateIndex
				break
			}
		}
		if remoteIndex == -1 {
			m.Rules = remote.Rules
			return nil
		}
		matched[remoteIndex] = true
		remoteRule := remote.Rules[remoteIndex]
		if m.Rules[i].Actions.IsUnknown() {
			m.Rules[i].Actions = remoteRule.Actions
		}
		if m.Rules[i].Users.IsUnknown() {
			m.Rules[i].Users = remoteRule.Users
		}
		if m.Rules[i].Groups.IsUnknown() {
			m.Rules[i].Groups = remoteRule.Groups
		}
		if m.Rules[i].ServiceAccounts.IsUnknown() {
			m.Rules[i].ServiceAccounts = remoteRule.ServiceAccounts
		}
	}
	return nil
}

func stringSetValues(ctx context.Context, value types.Set, diagnostics *diag.Diagnostics) []string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	var values []string
	diagnostics.Append(value.ElementsAs(ctx, &values, false)...)
	return values
}

func (m *AccessPolicyResourceModel) policyProto(ctx context.Context, name string) (*directoryv1alpha.AccessPolicy, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	policy := &directoryv1alpha.AccessPolicy{Name: name, Description: m.Description.ValueString()}
	policy.Rules = make([]*directoryv1alpha.AccessPolicyRule, 0, len(m.Rules))
	for _, rule := range m.Rules {
		policy.Rules = append(policy.Rules, &directoryv1alpha.AccessPolicyRule{
			Actions:         stringSetValues(ctx, rule.Actions, &diagnostics),
			Users:           stringSetValues(ctx, rule.Users, &diagnostics),
			Groups:          stringSetValues(ctx, rule.Groups, &diagnostics),
			ServiceAccounts: stringSetValues(ctx, rule.ServiceAccounts, &diagnostics),
		})
	}
	return policy, diagnostics
}

func accessPolicyRulesEqual(plan, state []AccessPolicyRuleResourceModel) bool {
	if len(plan) != len(state) {
		return false
	}
	matched := make([]bool, len(state))
	for _, plannedRule := range plan {
		match := -1
		for stateIndex, stateRule := range state {
			if !matched[stateIndex] && accessPolicyRuleMatches(plannedRule, stateRule) {
				match = stateIndex
				break
			}
		}
		if match == -1 {
			return false
		}
		matched[match] = true
	}
	return true
}

func accessPolicyRuleMatches(plan, state AccessPolicyRuleResourceModel) bool {
	return (plan.Actions.IsUnknown() || plan.Actions.Equal(state.Actions)) &&
		(plan.Users.IsUnknown() || plan.Users.Equal(state.Users)) &&
		(plan.Groups.IsUnknown() || plan.Groups.Equal(state.Groups)) &&
		(plan.ServiceAccounts.IsUnknown() || plan.ServiceAccounts.Equal(state.ServiceAccounts))
}

func (r *AccessPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data AccessPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, diagnostics := data.policyProto(ctx, "")
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	request := &directoryv1alpha.CreateAccessPolicyRequest{AccessPolicy: policy}
	request.SetParent("organizations/" + data.OrganizationID.ValueString())
	request.SetAccessPolicyId(data.AccessPolicyID.ValueString())
	expectedName := request.GetParent() + "/accessPolicies/" + request.GetAccessPolicyId()
	created, err := r.client.Directory.CreateAccessPolicy(ctx, request)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeAlreadyExists {
			resp.Diagnostics.AddError(
				"Access Policy Already Exists",
				fmt.Sprintf("An access policy already exists at %q. If a previous apply created the policy but lost the response, import it with `terraform import <resource-address> %s` before applying again.", expectedName, expectedName),
			)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	var responseErr error
	switch {
	case created == nil:
		responseErr = fmt.Errorf("access policy is missing")
	case created.GetName() != expectedName:
		responseErr = fmt.Errorf("access policy name %q does not match requested name %q", created.GetName(), expectedName)
	default:
		responseErr = data.setFromProto(created)
	}
	if responseErr != nil {
		remote, readErr := r.client.Directory.GetAccessPolicy(ctx, &directoryv1alpha.GetAccessPolicyRequest{Name: expectedName})
		if readErr != nil {
			resp.Diagnostics.AddError("Unable to Recover Access Policy After Creation", fmt.Sprintf("The Directory API accepted the create request for %q but returned an invalid response (%s), and reading it back failed: %s. Import the policy with `terraform import <resource-address> %s` before applying again.", expectedName, responseErr, readErr, expectedName))
			return
		}
		if readBackErr := data.setFromProto(remote); readBackErr != nil {
			resp.Diagnostics.AddError("Unable to Recover Access Policy After Creation", fmt.Sprintf("The Directory API accepted the create request for %q but both the create response and subsequent read were invalid: %s. Import the policy with `terraform import <resource-address> %s` before applying again.", expectedName, readBackErr, expectedName))
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AccessPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data AccessPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.Directory.GetAccessPolicy(ctx, &directoryv1alpha.GetAccessPolicyRequest{Name: data.Name.ValueString()})
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := data.setFromProto(policy); err != nil {
		resp.Diagnostics.AddError("Unexpected API Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AccessPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state AccessPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, diagnostics := plan.policyProto(ctx, state.Name.ValueString())
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	paths := make([]string, 0, 2)
	if !plan.Description.IsUnknown() && !plan.Description.Equal(state.Description) {
		paths = append(paths, "description")
	}
	if !accessPolicyRulesEqual(plan.Rules, state.Rules) {
		paths = append(paths, "rules")
	}
	if len(paths) == 0 {
		remote, err := r.client.Directory.GetAccessPolicy(ctx, &directoryv1alpha.GetAccessPolicyRequest{Name: state.Name.ValueString()})
		if err != nil {
			coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
			return
		}
		if err := plan.fillUnknownFromProto(remote); err != nil {
			resp.Diagnostics.AddError("Unexpected API Response", fmt.Sprintf("The Directory API returned an invalid access policy after a no-op update: %s.", err))
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}
	updated, err := r.client.Directory.UpdateAccessPolicy(ctx, &directoryv1alpha.UpdateAccessPolicyRequest{
		AccessPolicy: policy,
		UpdateMask:   &fieldmaskpb.FieldMask{Paths: paths},
	})
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := plan.setFromProto(updated); err != nil {
		remote, readErr := r.client.Directory.GetAccessPolicy(ctx, &directoryv1alpha.GetAccessPolicyRequest{Name: state.Name.ValueString()})
		if readErr != nil {
			coreweave.HandleAPIError(ctx, readErr, &resp.Diagnostics)
			return
		}
		if readBackErr := plan.setFromProto(remote); readBackErr != nil {
			resp.Diagnostics.AddError("Unexpected API Response", fmt.Sprintf("The Directory API returned an invalid access policy after update and the subsequent read could not recover it: %s.", readBackErr))
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AccessPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data AccessPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.Directory.DeleteAccessPolicy(ctx, &directoryv1alpha.DeleteAccessPolicyRequest{Name: data.Name.ValueString()})
	if err != nil && !coreweave.IsNotFoundError(err) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
	}
}

func (r *AccessPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	organizationID, accessPolicyID, err := parseAccessPolicyName(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Access Policy Import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), organizationID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("access_policy_id"), accessPolicyID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}

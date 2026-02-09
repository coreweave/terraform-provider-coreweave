package objectstorage

import (
	"bytes"
	"context"
	"fmt"

	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/zclconf/go-cty/cty"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &OrganizationAccessPolicyResource{}
	_ resource.ResourceWithImportState = &OrganizationAccessPolicyResource{}
)

const (
	orgAccessPolicyVersion string = "v1alpha1"
)

func NewOrganizationAccessPolicyResource() resource.Resource {
	return &OrganizationAccessPolicyResource{}
}

// OrganizationAccessPolicyResource defines the resource implementation.
type OrganizationAccessPolicyResource struct {
	client *coreweave.Client
}

type PolicyStatementResourceModel struct {
	// Name
	Name types.String `tfsdk:"name"`
	// Either "Allow" or "Deny"
	Effect types.String `tfsdk:"effect"`
	// Actions this statement covers, e.g. ["s3:GetObject", "s3:PutObject"]
	Actions types.Set `tfsdk:"actions"`
	// Resources this statement covers, e.g. ["my-bucket/*"]
	Resources types.Set `tfsdk:"resources"`
	// Principals this statement applies to, e.g. ["coreweave/UserUID"]
	Principals types.Set `tfsdk:"principals"`
}

func (p *PolicyStatementResourceModel) Set(statement *cwobjectv1.CWObjectPolicyStatement) {
	if statement == nil {
		return
	}

	p.Name = types.StringValue(statement.Name)
	p.Effect = types.StringValue(statement.Effect)

	p.Actions = types.SetNull(types.StringType)
	if len(statement.Actions) > 0 {
		actions := []attr.Value{}
		for _, a := range statement.Actions {
			actions = append(actions, types.StringValue(a))
		}

		p.Actions = types.SetValueMust(types.StringType, actions)
	}

	p.Principals = types.SetNull(types.StringType)
	if len(statement.Principals) > 0 {
		principals := []attr.Value{}
		for _, p := range statement.Principals {
			principals = append(principals, types.StringValue(p))
		}

		p.Principals = types.SetValueMust(types.StringType, principals)
	}

	p.Resources = types.SetNull(types.StringType)
	if len(statement.Resources) > 0 {
		resources := []attr.Value{}
		for _, r := range statement.Resources {
			resources = append(resources, types.StringValue(r))
		}

		p.Resources = types.SetValueMust(types.StringType, resources)
	}
}

type OrganizationAccessPolicyResourceModel struct {
	// The policy’s name
	Name types.String `tfsdk:"name"`
	// A list of policy statements
	Statements []PolicyStatementResourceModel `tfsdk:"statements"`
}

func (o *OrganizationAccessPolicyResourceModel) Set(policy *cwobjectv1.CWObjectPolicy) {
	if policy == nil {
		return
	}

	o.Name = types.StringValue(policy.Name)

	statements := []PolicyStatementResourceModel{}
	for _, s := range policy.Statements {
		statement := PolicyStatementResourceModel{}
		statement.Set(s)
		statements = append(statements, statement)
	}
	o.Statements = statements
}

func (o *OrganizationAccessPolicyResourceModel) ToEnsureAccessPolicyRequest(ctx context.Context, respDiag diag.Diagnostics) *cwobjectv1.EnsureAccessPolicyRequest {
	statements := []*cwobjectv1.CWObjectPolicyStatement{}

	for _, s := range o.Statements {
		actions := []string{}
		if diag := s.Actions.ElementsAs(ctx, &actions, false); diag.HasError() {
			respDiag.Append(diag...)
			return nil
		}

		principals := []string{}
		if diag := s.Principals.ElementsAs(ctx, &principals, false); diag.HasError() {
			respDiag.Append(diag...)
			return nil
		}

		resources := []string{}
		if diag := s.Resources.ElementsAs(ctx, &resources, false); diag.HasError() {
			respDiag.Append(diag...)
			return nil
		}

		statements = append(statements, &cwobjectv1.CWObjectPolicyStatement{
			Name:       s.Name.ValueString(),
			Effect:     s.Effect.ValueString(),
			Actions:    actions,
			Principals: principals,
			Resources:  resources,
		})
	}

	return &cwobjectv1.EnsureAccessPolicyRequest{
		Policy: &cwobjectv1.CWObjectPolicy{
			Name:       o.Name.ValueString(),
			Version:    orgAccessPolicyVersion,
			Statements: statements,
		},
	}
}

func (o *OrganizationAccessPolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage_organization_access_policy"
}

func (o *OrganizationAccessPolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "[Organization access policies](https://docs.coreweave.com/products/storage/object-storage/auth-access/organization-policies/about) enforce permissions for AI Object Storage across your entire CoreWeave organization, automatically covering every resource, bucket, and user in your account. At least one organization access policy must be in place before you can create a bucket.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the organization access policy, must be unique.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"statements": schema.SetNestedAttribute{
				Required:            true,
				MarkdownDescription: "The list of access policy statements associated with this policy. At least one statement is required.",
				Validators: []validator.Set{
					atLeastOneElementSetValidator{},
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "A short, human-readable identifier for this specific policy statement, similar to Sid in bucket access policies.",
						},
						"effect": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Must be either Allow or Deny (case-sensitive). Determines whether the statement grants or denies the specified actions on the listed resources for the designated principals. By default, all access is denied.",
							Validators: []validator.String{
								effectValidator{},
							},
						},
						"actions": schema.SetAttribute{
							Required:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Defines which operations the policy allows or denies. Organization access policies can include actions from two APIs - S3 (s3:*) and AI Object Storage API (cwobject:*). You can use wildcards (like s3:* or cwobject:*) to cover multiple actions at once.",
							Validators: []validator.Set{
								atLeastOneElementSetValidator{},
							},
						},
						"resources": schema.SetAttribute{
							Required:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Defines which resources the policy applies to. See the [AI Object Storage documentation](https://docs.coreweave.com/products/storage/object-storage/concepts/policies/organization-policies#resources) for guidelines on defining resources.",
							Validators: []validator.Set{
								atLeastOneElementSetValidator{},
							},
						},
						"principals": schema.SetAttribute{
							Required:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Defines which users, roles, or groups the policy applies to. Only short-form identifiers are supported. If you use a full ARN, the policy will fail with an error. See the [AI Object Storage documentation](https://docs.coreweave.com/products/storage/object-storage/concepts/policies/organization-policies#resources) for guidelines on defining principals.",
							Validators: []validator.Set{
								atLeastOneElementSetValidator{},
							},
						},
					},
				},
			},
		},
	}
}

func (o *OrganizationAccessPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	o.client = client
}

func (o *OrganizationAccessPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data OrganizationAccessPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	policyReq := data.ToEnsureAccessPolicyRequest(ctx, resp.Diagnostics)
	// if we failed to build a policy request, return & show the diagnostic error
	if policyReq == nil {
		return
	}

	_, err := o.client.EnsureAccessPolicy(ctx, connect.NewRequest(policyReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (o *OrganizationAccessPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OrganizationAccessPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	policies, err := o.client.ListAccessPolicies(ctx, &connect.Request[cwobjectv1.ListAccessPoliciesRequest]{})
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	for _, p := range policies.Msg.Policies {
		if p.Name == data.Name.ValueString() {
			data.Set(p)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
	}

	// if no policy exists with the given name, remove it from state & assume it has been deleted
	resp.State.RemoveResource(ctx)
}

func (o *OrganizationAccessPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data OrganizationAccessPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	policyReq := data.ToEnsureAccessPolicyRequest(ctx, resp.Diagnostics)
	// if we failed to build a policy request, return & show the diagnostic error
	if policyReq == nil {
		return
	}

	_, err := o.client.EnsureAccessPolicy(ctx, connect.NewRequest(policyReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (o *OrganizationAccessPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OrganizationAccessPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	_, err := o.client.DeleteAccessPolicy(ctx, connect.NewRequest(&cwobjectv1.DeleteAccessPolicyRequest{
		Name: data.Name.ValueString(),
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
}

func (o *OrganizationAccessPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	policies, err := o.client.ListAccessPolicies(ctx, &connect.Request[cwobjectv1.ListAccessPoliciesRequest]{})
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	for _, p := range policies.Msg.Policies {
		if p.Name == req.ID {
			data := OrganizationAccessPolicyResourceModel{}
			data.Set(p)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
	}

	resp.Diagnostics.AddError("Organization access policy not found", fmt.Sprintf("organization access policy with name %q not found, verify the name & try again. ", req.ID))
}

type effectValidator struct{}

var (
	_ validator.String = effectValidator{}
)

func (e effectValidator) Description(context.Context) string {
	return "Effect must be one of either 'Allow' or 'Deny' (case-sensitive)"
}
func (e effectValidator) MarkdownDescription(context.Context) string {
	return "Effect must be one of either 'Allow' or 'Deny' (case-sensitive)"
}
func (e effectValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	// Skip unknown or null values; Terraform will re-run once known.
	if req.ConfigValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}

	raw := req.ConfigValue.ValueString()
	if raw != "Allow" && raw != "Deny" {
		resp.Diagnostics.AddAttributeError(
			path.Root(req.Path.String()),
			"Invalid Effect",
			fmt.Sprintf(
				`Effect must be one of 'Allow' or 'Deny' (case-sensitive), but got: %q.`,
				raw,
			),
		)
	}
}

// MustRenderOrganizationAccessPolicy is a helper to render HCL for use in acceptance testing.
// It should not be used by clients of this library.
func MustRenderOrganizationAccessPolicy(ctx context.Context, resourceName string, policy *OrganizationAccessPolicyResourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{"coreweave_object_storage_organization_access_policy", resourceName})
	resourceBody := resource.Body()

	resourceBody.SetAttributeValue("name", cty.StringVal(policy.Name.ValueString()))

	statements := []cty.Value{}
	for _, s := range policy.Statements {
		actionsSlice := []string{}
		s.Actions.ElementsAs(ctx, &actionsSlice, false)
		actions := []cty.Value{}
		for _, a := range actionsSlice {
			actions = append(actions, cty.StringVal(a))
		}

		resourcesSlice := []string{}
		s.Resources.ElementsAs(ctx, &resourcesSlice, false)
		resources := []cty.Value{}
		for _, a := range resourcesSlice {
			resources = append(resources, cty.StringVal(a))
		}

		principalsSlice := []string{}
		s.Principals.ElementsAs(ctx, &principalsSlice, false)
		principals := []cty.Value{}
		for _, a := range principalsSlice {
			principals = append(principals, cty.StringVal(a))
		}

		statements = append(statements, cty.ObjectVal(map[string]cty.Value{
			"name":       cty.StringVal(s.Name.ValueString()),
			"effect":     cty.StringVal(s.Effect.ValueString()),
			"actions":    cty.SetVal(actions),
			"resources":  cty.SetVal(resources),
			"principals": cty.SetVal(principals),
		}))
	}

	resourceBody.SetAttributeValue("statements", cty.ListVal(statements))

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

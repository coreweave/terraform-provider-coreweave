package objectstorage

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type OrganizationAccessPolicyResourceModel struct {
	// The policy’s name
	Name types.String `tfsdk:"name"`
	// Internal policy version, e.g. "v1alpha1"
	Version types.String `tfsdk:"version"`
	// A list of policy statements
	Statements []PolicyStatementResourceModel `tfsdk:"statements"`
}

type PolicyStatementResourceModel struct {
	// Name
	Name types.String `tfsdk:"name"`
	// Either "Allow" or "Deny"
	Effect types.String `tfsdk:"effect"`
	// Actions this statement covers, e.g. ["s3:GetObject", "s3:PutObject"]
	Actions types.List `tfsdk:"actions"`
	// Resources this statement covers, e.g. ["arn:cwobject:::bucket/*"]
	Resources types.List `tfsdk:"resources"`
	// Principals this statement applies to, e.g. ["coreweave/UserUID"]
	Principals types.List `tfsdk:"principals"`
}

package telecaster

import (
	"fmt"

	telecastertypesv1beta1 "github.com/coreweave/o11y-mgmt/gen/cw/telecaster/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type SecretRefModel struct {
	Slug types.String `tfsdk:"slug"`
}

func (s *SecretRefModel) toProtoObject() *telecastertypesv1beta1.SecretRef {
	if s == nil {
		return nil
	}
	return &telecastertypesv1beta1.SecretRef{
		Slug: s.Slug.ValueString(),
	}
}

func secretRefSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "Reference to a Telecaster Secret to be used.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"slug": schema.StringAttribute{
				MarkdownDescription: "The slug of the secret.",
				Required:            true,
			},
		},
	}
}

func secretKeySchemaAttribute(forAttribute string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: fmt.Sprintf("The key within the secret to be used as the %s.", forAttribute),
		Required:            true,
	}
}

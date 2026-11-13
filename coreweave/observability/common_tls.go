package observability

import (
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func tlsConfigAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "Configuration for TLS connections.",
		Attributes: map[string]schema.Attribute{
			"certificate_authority_data": schema.StringAttribute{
				MarkdownDescription: "Base64 encoded CA certificate data.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, tlsCADataMaxLength),
				},
			},
		},
		Optional: true,
	}
}

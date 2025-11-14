package telecaster

import (
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

func tlsConfigModelAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "Configuration for TLS connections.",
		Attributes: map[string]schema.Attribute{
			"certificate_authority_data": schema.StringAttribute{
				MarkdownDescription: "Base64 encoded CA certificate data.",
				Required:            true,
			},
		},
		Optional: true,
	}
}

package telecaster

import (
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// basicAuthAttribute returns a reusable HTTP Basic authentication schema.
func basicAuthAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "HTTP Basic authentication credentials.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"username": schema.StringAttribute{
				MarkdownDescription: "Username for HTTP Basic authentication.",
				Required:            true,
				Sensitive:           true,
				// WriteOnly:           true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password for HTTP Basic authentication.",
				Required:            true,
				Sensitive:           true,
				WriteOnly:           true,
			},
		},
	}
}

// bearerTokenAttribute returns a reusable Bearer token authentication schema.
func bearerTokenAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "Bearer token authentication credentials.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				MarkdownDescription: "Bearer token value.",
				Required:            true,
				Sensitive:           true,
				WriteOnly:           true,
			},
		},
	}
}

// authHeadersAttribute returns a reusable custom HTTP headers authentication schema.
func authHeadersAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "Custom HTTP headers for authentication.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"headers": schema.MapAttribute{
				MarkdownDescription: "Map of HTTP header names to values for authentication.",
				Required:            true,
				Sensitive:           true,
				WriteOnly:           true,
				ElementType:         types.StringType,
			},
		},
	}
}

package observability

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
				WriteOnly:           true,
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

// s3CredentialsAttribute returns a reusable S3 credentials schema.
func s3CredentialsAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "AWS credentials for S3 bucket access.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"access_key_id": schema.StringAttribute{
				MarkdownDescription: "AWS Access Key ID for S3 authentication.",
				Required:            true,
				Sensitive:           true,
				// The API never returns credentials, so nothing can be read back into
				// state. WriteOnly matches every other credential attribute; flipping it
				// later would be breaking in either direction.
				WriteOnly: true,
			},
			"secret_access_key": schema.StringAttribute{
				MarkdownDescription: "AWS Secret Access Key for S3 authentication.",
				Required:            true,
				Sensitive:           true,
				WriteOnly:           true,
			},
			"session_token": schema.StringAttribute{
				MarkdownDescription: "AWS Session Token for temporary S3 credentials (optional).",
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
			},
		},
	}
}

// prometheusCredentialsAttribute returns a reusable Prometheus credentials schema.
func prometheusCredentialsAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		// auth_headers is absent on purpose. PrometheusCredentials carries the
		// field, but the API rejects it for Prometheus endpoints, so publishing it
		// would advertise a configuration that cannot apply. Adding it later is
		// additive.
		MarkdownDescription: "Authentication credentials for the Prometheus Remote Write endpoint. `basic_auth` is required when the block is present.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"basic_auth": basicAuthAttribute(),
		},
	}
}

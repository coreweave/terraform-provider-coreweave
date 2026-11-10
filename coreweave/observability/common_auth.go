package observability

import (
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, basicAuthUsernameMaxLength),
				},
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password for HTTP Basic authentication.",
				Required:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, basicAuthPasswordMaxLength),
				},
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
				MarkdownDescription: "Map of HTTP header names to values for authentication. " +
					"1 to 20 entries. Names must be unique ignoring case, since HTTP field names " +
					"are case-insensitive and the endpoint would receive the header twice. " +
					"`Host`, `Content-Length`, `Content-Type`, `Connection` and `Transfer-Encoding` " +
					"are set by the forwarder and cannot be overridden. Neither names nor values may " +
					"contain a comma or `${`, and values cannot begin or end with whitespace.",
				Required:    true,
				Sensitive:   true,
				WriteOnly:   true,
				ElementType: types.StringType,
				Validators: []validator.Map{
					mapvalidator.SizeBetween(authHeadersMinPairs, authHeadersMaxPairs),
					mapvalidator.KeysAre(
						stringvalidator.LengthBetween(1, authHeaderNameMaxLength),
						stringvalidator.RegexMatches(authHeaderNamePat, "must be a valid HTTP header name"),
					),
				},
			},
		},
	}
}

// s3CredentialsAttribute returns a reusable S3 credentials schema.
//
// Optional in the schema but mandatory today: ValidateConfig rejects a config
// without it. Mandatory-ness belongs to the auth mode rather than the schema —
// workload identity derives credentials from the connector's own JWT and stores
// none — so marking this Required would have to be undone, which is the
// breaking direction, once an auth_mode discriminator exists.
func s3CredentialsAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "AWS credentials for S3 bucket access. Required: S3 endpoints cannot be created without credentials, and credentials cannot be removed afterwards.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"access_key_id": schema.StringAttribute{
				MarkdownDescription: "AWS Access Key ID for S3 authentication.",
				Required:            true,
				Sensitive:           true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(s3AccessKeyIDMinLength, s3AccessKeyIDMaxLength),
					stringvalidator.RegexMatches(s3AccessKeyIDPat, "must contain only uppercase letters and digits"),
				},
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
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, s3SecretAccessKeyMaxLength),
				},
			},
			"session_token": schema.StringAttribute{
				MarkdownDescription: "AWS Session Token for temporary S3 credentials (optional).",
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Validators: []validator.String{
					stringvalidator.LengthAtMost(s3SessionTokenMaxLength),
				},
			},
		},
	}
}

package provider

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// durationValidator implements validator.String. It will check that a non-null,
// non-unknown string can be parsed by time.ParseDuration.
type durationValidator struct{}

// Description is a short summary for “terraform plan/tfdocs” output.
func (v durationValidator) Description(ctx context.Context) string {
	return "Must be a valid Go duration (e.g. \"100ms\", \"5s\", \"1h30m\"). If an integer is passed, assumed as seconds (e.g. \"100\" -> \"100s\")"
}

// MarkdownDescription is the same, but rendered in Markdown in docs.
func (v durationValidator) MarkdownDescription(ctx context.Context) string {
	return "Must be a valid Go duration (e.g. \"100ms\", \"5s\", \"1h30m\"). If an integer is passed, assumed as seconds (e.g. \"100\" -> \"100s\")"
}

// ValidateString is called during plan/apply. If the value is known & non-null,
// we attempt to parse it via time.ParseDuration. On error, we add a Diagnostics entry.
func (v durationValidator) ValidateString(
	ctx context.Context,
	req validator.StringRequest,
	resp *validator.StringResponse,
) {
	// If the attribute value is unknown (depends on interpolation) or null,
	// skip validation now. Terraform will re-run validation once it becomes known.
	if req.ConfigValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}

	raw := req.ConfigValue.ValueString()
	if _, err := time.ParseDuration(raw); err != nil {
		// Try appending “s” to treat it as seconds
		if _, err2 := time.ParseDuration(raw + "s"); err2 == nil {
			return
		}

		// AddAttributeError takes: (path, summary, detail)
		resp.Diagnostics.AddAttributeError(
			path.Root(req.Path.String()),
			"Invalid duration format",
			// The user saw e.g. "5xs" or "abc"; show what they passed and a hint.
			`Expected a valid Go duration (for example: "5s", "250ms", or "1h"), but got: "`+raw+`".`,
		)
	}
}

// uriValidator implements validator.String. It will check that a non-null,
// non-unknown string can be parsed by url.ParseRequestURI and has a scheme & host.
type uriValidator struct{}

// Description is a short summary for “terraform plan/tfdocs” output.
func (v uriValidator) Description(ctx context.Context) string {
	return "Must be a valid URI (e.g. \"https://example.com/path?query=1\")"
}

// MarkdownDescription is the same, but rendered in Markdown in docs.
func (v uriValidator) MarkdownDescription(ctx context.Context) string {
	return "Must be a valid URI (for example: \"https://example.com/path?query=1\")."
}

// ValidateString is called during plan/apply. If the value is known & non-null,
// we attempt to parse it via url.ParseRequestURI. On error or missing parts,
// we add a Diagnostics entry.
func (v uriValidator) ValidateString(
	ctx context.Context,
	req validator.StringRequest,
	resp *validator.StringResponse,
) {
	// Skip unknown or null values; Terraform will re-run once known.
	if req.ConfigValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}

	raw := req.ConfigValue.ValueString()
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root(req.Path.String()),
			"Invalid URI",
			fmt.Sprintf(
				`Expected a valid URI (with scheme and host), for example "%s", but got: %q.`,
				CoreweaveApiEndpointDefault,
				raw,
			),
		)
	}
}

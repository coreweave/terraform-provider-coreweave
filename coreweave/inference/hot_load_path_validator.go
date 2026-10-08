package inference

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type hotLoadPathValidator struct{}

func (hotLoadPathValidator) Description(context.Context) string {
	return "must not contain path traversal (..) segments"
}

func (v hotLoadPathValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v hotLoadPathValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for _, segment := range strings.Split(req.ConfigValue.ValueString(), "/") {
		if segment == ".." {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid checkpoint path prefix", v.Description(ctx))
			return
		}
	}
}

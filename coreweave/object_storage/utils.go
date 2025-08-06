package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/smithy-go/transport/http"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func isTransientS3Error(err error) bool {
	var httpErr *http.ResponseError
	if errors.As(err, &httpErr) {
		switch httpErr.Response.StatusCode {
		case 404, 408, 429, 500, 502, 503, 504:
			return true
		}
		return false
	}

	return false
}

type atLeastOneElementSetValidator struct{}

var (
	_ validator.Set = atLeastOneElementSetValidator{}
)

func (e atLeastOneElementSetValidator) Description(ctx context.Context) string {
	return "Must contain at least one element"
}

func (e atLeastOneElementSetValidator) MarkdownDescription(ctx context.Context) string {
	// reuse the plain text description
	return e.Description(ctx)
}

func (e atLeastOneElementSetValidator) ValidateSet(ctx context.Context, req validator.SetRequest, resp *validator.SetResponse) {
	// skip until the value is known and non-null
	if req.ConfigValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}

	if len(req.ConfigValue.Elements()) < 1 {
		// grab just the final segment of the path
		full := req.Path.String()
		parts := strings.Split(full, ".")
		last := parts[len(parts)-1]

		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Empty set value",
			fmt.Sprintf("The %q set must contain at least one element.", last),
		)
	}
}

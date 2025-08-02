package objectstorage

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// pollUntil runs check(ctx) every interval until it returns (true, nil),
// or else returns the first non‐nil error, or a timeout error.
func pollUntil(operation string, parentCtx context.Context, interval, timeout time.Duration, check func(ctx context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out polling for %s after %v: %w", operation, timeout, ctx.Err())
		case <-ticker.C:
			ok, err := check(ctx)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		}
	}
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
		// Short summary, then a detailed message including the attribute name
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Empty set value",
			fmt.Sprintf(
				"The configuration attribute %q must contain at least one element, but it was empty.",
				req.Path,
			),
		)
	}
}

package networking

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
)

// hostPrefixesAppendOnly rejects unsupported mutations instead of requesting
// replacement, which could also replace resources depending on the VPC.
func hostPrefixesAppendOnly() planmodifier.Set { return hostPrefixesAppendOnlyModifier{} }

type hostPrefixesAppendOnlyModifier struct{}

func (hostPrefixesAppendOnlyModifier) Description(context.Context) string {
	return "Allows adding host prefixes while preserving every existing element."
}

func (m hostPrefixesAppendOnlyModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (hostPrefixesAppendOnlyModifier) PlanModifySet(ctx context.Context, req planmodifier.SetRequest, resp *planmodifier.SetResponse) {
	// Creation, destruction, and an omitted optional/computed attribute do not
	// request mutation of an existing host prefix set.
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || req.ConfigValue.IsNull() || req.StateValue.IsNull() {
		return
	}
	planned, err := req.PlanValue.ToTerraformValue(ctx)
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid host prefixes", err.Error())
		return
	}
	// Terraform plans again once unknown inputs resolve. The API independently
	// enforces the same append-only constraint at apply time.
	if !planned.IsFullyKnown() {
		return
	}
	for _, existing := range req.StateValue.Elements() {
		found := false
		for _, proposed := range req.PlanValue.Elements() {
			if existing.Equal(proposed) {
				found = true
				break
			}
		}
		if !found {
			resp.Diagnostics.AddAttributeError(req.Path, "Unsupported host prefix change",
				fmt.Sprintf("Existing host prefixes cannot be removed or modified. "+
					"Only new named host prefixes may be added. Preserve this existing element: %s. "+
					"The provider will not replace the VPC for this change.", existing.String()))
			return
		}
	}
}

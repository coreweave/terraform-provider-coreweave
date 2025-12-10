package telecaster_test

import (
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// setCommonEndpointAttributes sets common writable attributes from ForwardingEndpointModelCore
// that are present in all endpoint types (slug, display_name).
func setCommonEndpointAttributes(body *hclwrite.Body, core model.ForwardingEndpointCore) {
	body.SetAttributeValue("slug", cty.StringVal(core.Slug.ValueString()))
	body.SetAttributeValue("display_name", cty.StringVal(core.DisplayName.ValueString()))
}

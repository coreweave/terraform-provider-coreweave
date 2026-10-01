package cks

import (
	"context"
	"slices"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var publicAccessAttrTypes = map[string]attr.Type{
	"mode":        types.StringType,
	"allow_cidrs": types.SetType{ElemType: types.StringType},
}

func publicAccessToProto(value types.Object) *cksv1beta1.PublicAccessConfig {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}

	attrs := value.Attributes()
	elements := attrs["allow_cidrs"].(types.Set).Elements()
	cidrs := make([]string, len(elements))
	for i, value := range elements {
		cidrs[i] = value.(types.String).ValueString()
	}
	slices.Sort(cidrs)

	config := &cksv1beta1.PublicAccessConfig{AllowCidrs: cidrs}
	if mode := attrs["mode"].(types.String); !mode.IsNull() {
		config.Mode = cksv1beta1.PublicAccessConfig_Mode(cksv1beta1.PublicAccessConfig_Mode_value[mode.ValueString()]).Enum()
	}

	return config
}

func publicAccessFromProto(config *cksv1beta1.PublicAccessConfig) types.Object {
	if config == nil {
		return types.ObjectNull(publicAccessAttrTypes)
	}

	values := make([]attr.Value, len(config.AllowCidrs))
	for i, cidr := range config.AllowCidrs {
		values[i] = types.StringValue(cidr)
	}

	mode := types.StringNull()
	if config.Mode != nil {
		mode = types.StringValue(config.Mode.String())
	}

	return types.ObjectValueMust(publicAccessAttrTypes, map[string]attr.Value{
		"mode":        mode,
		"allow_cidrs": types.SetValueMust(types.StringType, values),
	})
}

func requireKnownPublicAccess(ctx context.Context, value types.Object) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	terraformValue, err := value.ToTerraformValue(ctx)
	if err != nil || !terraformValue.IsFullyKnown() {
		diagnostics.AddAttributeError(path.Root("public_access"), "Unknown public access configuration",
			"All public_access values must be known before applying the cluster configuration.")
	}

	return diagnostics
}

func validatePublicAccessForApply(ctx context.Context, value types.Object) diag.Diagnostics {
	diagnostics := requireKnownPublicAccess(ctx, value)
	if diagnostics.HasError() || value.IsNull() {
		return diagnostics
	}

	diagnostics.Append(validatePublicAccess(value, path.Root("public_access"))...)

	return diagnostics
}

func validatePublicAccessUpgrade(plan, state *ClusterResourceModel) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if !plan.Version.Equal(state.Version) && !plan.PublicAccess.Equal(state.PublicAccess) {
		diagnostics.AddAttributeError(path.Root("public_access"), "Public access cannot change during a Kubernetes upgrade",
			"Apply public_access changes separately from a Kubernetes version change.")
	}

	return diagnostics
}

package cks

import (
	"context"
	"net/netip"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	publicAccessDescription     = "Direct public access to the cluster's Kubernetes API server. CoreWeave-managed authentication is not currently supported. The allowlist applies only to this endpoint; legacy ingress is controlled independently by public. Removing this attribute clears direct public access configuration. Changes to this configuration and Kubernetes version upgrades must be applied separately."
	publicAccessModeDescription = "Access mode for the direct public endpoint: TLS enables TLS access to the Kubernetes API server; DISABLED disables this endpoint. Omitted or MODE_UNSPECIFIED behaves as DISABLED, but mode presence is preserved in state. Legacy public ingress is unaffected. TLS requires a nonempty allow_cidrs set."
	allowCIDRsDescription       = "Up to 100 IPv4 or IPv6 CIDR ranges allowed through the direct public endpoint. At least one is required when mode is TLS; no ranges are supplied automatically. Include 0.0.0.0/0 or ::/0 explicitly to allow all sources for that address family. Kubernetes authentication and authorization still apply. Omitted, null, or empty CIDRs are allowed only when disabled."
	legacyPublicDescription     = "Whether to expose the cluster's Kubernetes API server through legacy public ingress, including CoreWeave-managed authentication. This remains supported and operates independently of public_access; its allowlist does not restrict legacy ingress."
)

func publicAccessResourceAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: publicAccessDescription,
		Validators:          []validator.Object{publicAccessValidator{}},
		Attributes: map[string]schema.Attribute{
			"mode": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: publicAccessModeDescription,
			},
			"allow_cidrs": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: allowCIDRsDescription,
				// Protobuf repeated fields cannot preserve null versus empty in state.
				Default: setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
			},
		},
	}
}

type publicAccessValidator struct{}

func (publicAccessValidator) Description(context.Context) string {
	return "Select MODE_UNSPECIFIED, DISABLED, or TLS and allow at most 100 valid IPv4 or IPv6 CIDR prefixes; at least one is required for TLS."
}

func (v publicAccessValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (publicAccessValidator) ValidateObject(_ context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	resp.Diagnostics.Append(validatePublicAccess(req.ConfigValue, req.Path)...)
}

func validatePublicAccess(value types.Object, attributePath path.Path) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diagnostics
	}

	attrs := value.Attributes()
	mode := attrs["mode"].(types.String)
	if !mode.IsNull() && !mode.IsUnknown() {
		switch mode.ValueString() {
		case "MODE_UNSPECIFIED", "DISABLED", "TLS":
		default:
			diagnostics.AddAttributeError(attributePath.AtName("mode"), "Invalid public access mode",
				"mode must be one of MODE_UNSPECIFIED, DISABLED, or TLS.")
		}
	}
	cidrs := attrs["allow_cidrs"].(types.Set)
	if cidrs.IsUnknown() {
		return diagnostics
	}

	cidrPath := attributePath.AtName("allow_cidrs")
	elements := cidrs.Elements()
	if mode.ValueString() == "TLS" && len(elements) == 0 {
		diagnostics.AddAttributeError(cidrPath, "Missing public access CIDRs", "TLS public access requires at least one CIDR in allow_cidrs.")
	}

	fullyKnown := true
	for _, element := range elements {
		cidr := element.(types.String)
		if cidr.IsUnknown() {
			fullyKnown = false
			continue
		}
		if _, err := netip.ParsePrefix(cidr.ValueString()); err != nil {
			diagnostics.AddAttributeError(cidrPath, "Invalid CIDR prefix", "Each allow_cidrs element must be a non-null, valid IPv4 or IPv6 CIDR prefix.")
		}
	}

	// Unknown set members can resolve to duplicates, changing the final count.
	if fullyKnown && len(elements) > 100 {
		diagnostics.AddAttributeError(cidrPath, "Too many public access CIDRs", "allow_cidrs must contain at most 100 CIDR prefixes.")
	}

	return diagnostics
}

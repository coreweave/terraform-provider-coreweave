package cks

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/netip"
	"slices"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// maxKubeAPIAccessCIDRs is the API limit on entries in each of allow_cidrs and deny_cidrs.
const maxKubeAPIAccessCIDRs = 1024

var publicKubeAPIAccessAttrTypes = map[string]attr.Type{
	"allow_cidrs": types.SetType{ElemType: types.StringType},
	"deny_cidrs":  types.SetType{ElemType: types.StringType},
}

var kubeAPIAccessAttrTypes = map[string]attr.Type{
	"public": types.ObjectType{AttrTypes: publicKubeAPIAccessAttrTypes},
}

var publicKubeAPIAccessStatusAttrTypes = map[string]attr.Type{
	"endpoint": types.StringType,
	"status":   types.StringType,
}

var kubeAPIAccessStatusAttrTypes = map[string]attr.Type{
	"public":  types.ObjectType{AttrTypes: publicKubeAPIAccessStatusAttrTypes},
	"ca_data": types.StringType,
}

// kubeAPIAccessToProto converts configured access into its request message.
// A null value returns nil; a present value keeps the presence of its public object.
// CIDRs are sorted so requests serialize deterministically.
func kubeAPIAccessToProto(value types.Object) *cksv1beta1.KubeAPIAccess {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}

	access := &cksv1beta1.KubeAPIAccess{}
	public, ok := value.Attributes()["public"].(types.Object)
	if !ok || public.IsNull() || public.IsUnknown() {
		return access
	}

	attrs := public.Attributes()
	access.Public = &cksv1beta1.PublicKubeAPIAccess{
		AllowCidrs: cidrSetToProto(attrs["allow_cidrs"]),
		DenyCidrs:  cidrSetToProto(attrs["deny_cidrs"]),
	}

	return access
}

func cidrSetToProto(value attr.Value) []string {
	set, ok := value.(types.Set)
	if !ok || set.IsNull() || set.IsUnknown() {
		return nil
	}

	elements := set.Elements()
	cidrs := make([]string, 0, len(elements))
	for _, element := range elements {
		if cidr, ok := element.(types.String); ok && !cidr.IsNull() && !cidr.IsUnknown() {
			cidrs = append(cidrs, cidr.ValueString())
		}
	}
	slices.Sort(cidrs)

	return cidrs
}

// kubeAPIAccessFromProto converts API access configuration into Terraform state.
// CIDR sets inside a present public object are never null.
func kubeAPIAccessFromProto(access *cksv1beta1.KubeAPIAccess) types.Object {
	if access == nil {
		return types.ObjectNull(kubeAPIAccessAttrTypes)
	}

	public := types.ObjectNull(publicKubeAPIAccessAttrTypes)
	if access.Public != nil {
		public = types.ObjectValueMust(publicKubeAPIAccessAttrTypes, map[string]attr.Value{
			"allow_cidrs": cidrSetFromProto(access.Public.AllowCidrs),
			"deny_cidrs":  cidrSetFromProto(access.Public.DenyCidrs),
		})
	}

	return types.ObjectValueMust(kubeAPIAccessAttrTypes, map[string]attr.Value{"public": public})
}

// cidrSetFromProto drops duplicate entries, which Terraform rejects in a set.
func cidrSetFromProto(cidrs []string) types.Set {
	seen := make(map[string]struct{}, len(cidrs))
	values := make([]attr.Value, 0, len(cidrs))
	for _, cidr := range cidrs {
		if _, ok := seen[cidr]; ok {
			continue
		}
		seen[cidr] = struct{}{}
		values = append(values, types.StringValue(cidr))
	}

	return types.SetValueMust(types.StringType, values)
}

// kubeAPIAccessStatusFromProto converts observed access status into Terraform state.
// Absent observations are null. The CA bundle is base64 encoded once from the raw PEM bytes.
func kubeAPIAccessStatusFromProto(status *cksv1beta1.KubeAPIAccessStatus) types.Object {
	if status == nil {
		return types.ObjectNull(kubeAPIAccessStatusAttrTypes)
	}

	public := types.ObjectNull(publicKubeAPIAccessStatusAttrTypes)
	if status.Public != nil {
		public = types.ObjectValueMust(publicKubeAPIAccessStatusAttrTypes, map[string]attr.Value{
			"endpoint": types.StringPointerValue(status.Public.Endpoint),
			// String() reports unrecognized future values by number rather than failing the read.
			"status": types.StringValue(status.Public.Status.String()),
		})
	}

	caData := types.StringNull()
	if len(status.CaData) > 0 {
		caData = types.StringValue(base64.StdEncoding.EncodeToString(status.CaData))
	}

	return types.ObjectValueMust(kubeAPIAccessStatusAttrTypes, map[string]attr.Value{
		"public":  public,
		"ca_data": caData,
	})
}

// validateKubeAPIAccess checks the known parts of a kube_api_access value.
// Unknown values are skipped so that configuration validation can defer them.
func validateKubeAPIAccess(value types.Object, attributePath path.Path) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diagnostics
	}

	public, ok := value.Attributes()["public"].(types.Object)
	if !ok || public.IsNull() || public.IsUnknown() {
		return diagnostics
	}

	publicPath := attributePath.AtName("public")
	attrs := public.Attributes()
	for _, name := range []string{"allow_cidrs", "deny_cidrs"} {
		set, ok := attrs[name].(types.Set)
		if !ok {
			continue
		}
		diagnostics.Append(validateCIDRSet(set, publicPath.AtName(name))...)
	}

	return diagnostics
}

func validateCIDRSet(set types.Set, attributePath path.Path) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if set.IsNull() || set.IsUnknown() {
		return diagnostics
	}

	elements := set.Elements()
	fullyKnown := true
	for _, element := range elements {
		cidr, ok := element.(types.String)
		switch {
		case !ok || cidr.IsNull():
			diagnostics.AddAttributeError(attributePath, "Invalid CIDR prefix",
				"Each entry must be a non-null IPv4 or IPv6 network prefix, such as 203.0.113.0/24 or 2001:db8::/32.")
		case cidr.IsUnknown():
			fullyKnown = false
		default:
			if !isNetworkPrefix(cidr.ValueString()) {
				diagnostics.AddAttributeError(attributePath, "Invalid CIDR prefix",
					fmt.Sprintf("%q is not a valid network prefix. Each entry must be an IPv4 or IPv6 network address "+
						"with a prefix length and no host bits set, such as 203.0.113.0/24 or 2001:db8::/32.", cidr.ValueString()))
			}
		}
	}

	// Unknown members can resolve to duplicates, so the final count is only known once all members are.
	if fullyKnown && len(elements) > maxKubeAPIAccessCIDRs {
		diagnostics.AddAttributeError(attributePath, "Too many CIDR prefixes",
			fmt.Sprintf("Each CIDR set may contain at most %d prefixes; got %d.", maxKubeAPIAccessCIDRs, len(elements)))
	}

	return diagnostics
}

// isNetworkPrefix reports whether value is an IPv4 or IPv6 prefix with no host bits set.
// The spelling is not normalized, so any valid form is accepted and sent unchanged.
func isNetworkPrefix(value string) bool {
	prefix, err := netip.ParsePrefix(value)
	return err == nil && prefix.Masked() == prefix
}

// validateKubeAPIAccessForApply rejects unresolved or invalid access configuration before any RPC.
func validateKubeAPIAccessForApply(ctx context.Context, value types.Object) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	attributePath := path.Root("kube_api_access")

	terraformValue, err := value.ToTerraformValue(ctx)
	if err != nil || !terraformValue.IsFullyKnown() {
		diagnostics.AddAttributeError(attributePath, "Unknown Kubernetes API access configuration",
			"All kube_api_access values must be known before the cluster can be created or updated.")
		return diagnostics
	}

	diagnostics.Append(validateKubeAPIAccess(value, attributePath)...)

	return diagnostics
}

// validateKubeAPIAccessUpgrade rejects access changes combined with a Kubernetes version change.
func validateKubeAPIAccessUpgrade(plan, state *ClusterResourceModel) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if !plan.Version.Equal(state.Version) && !plan.KubeAPIAccess.Equal(state.KubeAPIAccess) {
		diagnostics.AddAttributeError(path.Root("kube_api_access"), "Kubernetes API access cannot change during a Kubernetes upgrade",
			"Apply kube_api_access changes separately from a Kubernetes version change.")
	}

	return diagnostics
}

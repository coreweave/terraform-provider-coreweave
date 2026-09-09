package sandbox

import (
	"context"
	"fmt"
	"net/netip"

	sandboxv1 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func containsUnknown(value attr.Value) bool {
	if value.IsUnknown() {
		return true
	}
	switch v := value.(type) {
	case types.Object:
		for _, child := range v.Attributes() {
			if containsUnknown(child) {
				return true
			}
		}
	case types.Map:
		for _, child := range v.Elements() {
			if containsUnknown(child) {
				return true
			}
		}
	case types.List:
		for _, child := range v.Elements() {
			if containsUnknown(child) {
				return true
			}
		}
	case types.Set:
		for _, child := range v.Elements() {
			if containsUnknown(child) {
				return true
			}
		}
	}
	return false
}

func (r *ManagedRunnerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data managedRunnerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !data.Spec.IsNull() && !containsUnknown(data.Spec) {
		spec := &sandboxv1.ManagedRunnerSpec{}
		if err := objectToProto(data.Spec, spec); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("spec"), "Invalid Runner Configuration", err.Error())
		} else if err := validateSpec(spec); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("spec"), "Invalid Runner Configuration", err.Error())
		}
	}
	if !data.Policy.IsNull() && !containsUnknown(data.Policy) {
		policy := &sandboxv1.Policy{}
		if err := objectToProto(data.Policy, policy); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("policy"), "Invalid Runner Policy", err.Error())
		} else if err := validatePolicy(policy); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("policy"), "Invalid Runner Policy", err.Error())
		}
	}
}

func validateSpec(spec *sandboxv1.ManagedRunnerSpec) error {
	if plane := spec.GetDataPlane(); plane != nil && plane.GetExposure() == nil {
		return fmt.Errorf("data_plane must select exactly one of disabled, cluster_ip, load_balancer, or custom")
	}
	for _, exclusion := range spec.GetMaintenancePolicy().GetExclusions() {
		if exclusion.GetStartTime() == nil || exclusion.GetEndTime() == nil || !exclusion.EndTime.AsTime().After(exclusion.StartTime.AsTime()) {
			return fmt.Errorf("maintenance exclusion end_time must be after start_time")
		}
	}
	if scaling := spec.GetOverrides().GetScaling(); scaling != nil && scaling.MaxReplicas != 0 && scaling.MinReplicas > scaling.MaxReplicas {
		return fmt.Errorf("scaling min_replicas must not exceed max_replicas")
	}
	return nil
}

func validatePolicy(policy *sandboxv1.Policy) error {
	var prefixes []netip.Prefix
	for _, cidr := range policy.GetControlPlaneAccess().GetSourceIpAllowlist().GetCidrs() {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return fmt.Errorf("invalid source IP prefix %q", cidr)
		}
		prefixes = append(prefixes, prefix)
	}
	for i, prefix := range prefixes {
		for j, other := range prefixes {
			if i != j && prefix.Addr().BitLen() == other.Addr().BitLen() && other.Bits() <= prefix.Bits() && other.Contains(prefix.Addr()) {
				return fmt.Errorf("source IP prefix %s is already covered by %s; remove the redundant prefix", prefix, other)
			}
		}
	}
	network := policy.GetConstraints().GetNetwork()
	for _, rules := range [][]*sandboxv1.EgressRule{network.GetAllowedEgress(), network.GetDefaultEgress()} {
		for _, rule := range rules {
			if rule.GetDestination() == nil {
				return fmt.Errorf("each egress rule must select exactly one destination")
			}
			if err := validatePorts(rule.GetPorts()); err != nil {
				return err
			}
		}
	}
	for _, rule := range network.GetDefaultEgress() {
		if rule.GetDnsName() != "" {
			return fmt.Errorf("default_egress must not contain DNS-name destinations")
		}
	}
	for _, rules := range [][]*sandboxv1.IngressRule{network.GetAllowedIngress(), network.GetDefaultIngress()} {
		for _, rule := range rules {
			if rule.GetSource() == nil {
				return fmt.Errorf("each ingress rule must select exactly one source")
			}
			if err := validatePorts(rule.GetPorts()); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePorts(ports []*sandboxv1.PortRange) error {
	for _, port := range ports {
		if port.EndPort != 0 && port.EndPort < port.Port {
			return fmt.Errorf("end_port must be greater than or equal to port")
		}
	}
	return nil
}

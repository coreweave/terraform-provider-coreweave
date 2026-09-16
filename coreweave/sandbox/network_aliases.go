package sandbox

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func legacyNetworkAliases(message protoreflect.FullName) map[string]string {
	switch message {
	case "coreweave.sandbox.v1.NetworkConstraints":
		return map[string]string{"deny_dns": "deny_https_hostname_rules"}
	case "coreweave.sandbox.v1.EgressRule":
		return map[string]string{"dns_name": "https_hostname", "dns_name_except": "https_hostname_except"}
	default:
		return nil
	}
}

func canonicalizeNetworkAliases(object map[string]any, message protoreflect.FullName) error {
	for old, current := range legacyNetworkAliases(message) {
		value, ok := object[old]
		if !ok {
			continue
		}
		if _, exists := object[current]; exists {
			return fmt.Errorf("cannot set both %s and %s", old, current)
		}
		object[current] = value
		delete(object, old)
	}
	return nil
}

// Only typed policy network fields are aliases. Keys in the opaque base object
// (including Kubernetes labels and environment variables) must remain literal.
func canonicalizePolicyNetworkAliases(raw any) error {
	policy, _ := raw.(map[string]any)
	constraints, _ := policy["constraints"].(map[string]any)
	network, _ := constraints["network"].(map[string]any)
	if err := canonicalizeNetworkAliases(network, "coreweave.sandbox.v1.NetworkConstraints"); err != nil {
		return err
	}
	for _, name := range []string{"allowed_egress", "default_egress"} {
		rules, _ := network[name].([]any)
		for i, rawRule := range rules {
			rule, _ := rawRule.(map[string]any)
			if err := canonicalizeNetworkAliases(rule, "coreweave.sandbox.v1.EgressRule"); err != nil {
				return fmt.Errorf("%s[%d]: %w", name, i, err)
			}
		}
	}
	return nil
}

// Refresh must preserve a configured legacy attribute instead of moving its
// value into a new optional attribute and producing perpetual plan drift.
// Imports have no previous spelling and use the new canonical attributes.
func preserveNetworkAliasSpelling(raw map[string]any, message protoreflect.FullName, previous types.Object) {
	prior := previous.Attributes()
	for old, current := range legacyNetworkAliases(message) {
		value := prior[old]
		if value == nil || value.IsNull() || value.IsUnknown() {
			continue
		}
		if currentValue, ok := raw[current]; ok {
			raw[old] = currentValue
			delete(raw, current)
		}
	}
}

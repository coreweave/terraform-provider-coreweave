// Package provider exposes the CoreWeave Terraform provider for in-process use.
package provider

import (
	internalprovider "github.com/coreweave/terraform-provider-coreweave/internal/provider"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
)

// New returns a fresh, unconfigured provider. The version is reported in
// provider metadata and the User-Agent sent to the CoreWeave API.
func New(version string) fwprovider.Provider {
	return internalprovider.New(version)()
}

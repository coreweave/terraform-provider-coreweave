package provider_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/provider"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
)

func TestNew(t *testing.T) {
	t.Parallel()

	first := provider.New("1.0.0")
	if first == provider.New("1.0.0") {
		t.Fatal("New returned the same instance twice")
	}
	second := provider.New("2.0.0")
	for version, instance := range map[string]fwprovider.Provider{"1.0.0": first, "2.0.0": second} {
		var metadata fwprovider.MetadataResponse
		instance.Metadata(t.Context(), fwprovider.MetadataRequest{}, &metadata)
		if metadata.TypeName != "coreweave" || metadata.Version != version {
			t.Errorf("got provider %q version %q, want coreweave version %q", metadata.TypeName, metadata.Version, version)
		}
	}

	var schema fwprovider.SchemaResponse
	first.Schema(t.Context(), fwprovider.SchemaRequest{}, &schema)
	if schema.Diagnostics.HasError() {
		t.Fatalf("reading provider schema: %v", schema.Diagnostics)
	}
	if _, ok := schema.Schema.Attributes["token"]; !ok {
		t.Error("provider schema is missing the token attribute")
	}
}

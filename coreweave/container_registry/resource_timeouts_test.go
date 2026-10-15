package containerregistry_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

// TestFrameworkTimeoutValidation verifies the standard timeout schema rejects malformed durations before writes.
func TestFrameworkTimeoutValidation(t *testing.T) {
	for _, kind := range []string{"namespace", "access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			for _, action := range []string{"create", "read", "update", "delete"} {
				t.Run(action, func(t *testing.T) {
					var values map[string]tftypes.Value
					require.NoError(t, h.config(nil, false).As(&values))
					timeoutType := values["timeouts"].Type()
					timeouts := nullObject(timeoutType)
					timeouts[action] = tftypes.NewValue(tftypes.String, "not-a-duration")
					values["timeouts"] = tftypes.NewValue(timeoutType, timeouts)
					response, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: h.name, Config: h.dv(tftypes.NewValue(h.typ, values))})
					require.NoError(t, err)
					require.NotEmpty(t, response.Diagnostics)
					require.Contains(t, response.Diagnostics[0].Summary, "Time Duration")
					require.Empty(t, h.fake.creates)
					require.Empty(t, h.fake.deletes)
				})
			}
		})
	}
}

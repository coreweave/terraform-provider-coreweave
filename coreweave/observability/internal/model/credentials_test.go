package model_test

import (
	"testing"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPSCredentialsToMsg pins which oneof arm the auth mode selects, and
// the two shapes that have no arm at all. Credentials are write-only, so this
// conversion is the only place the choice is made and nothing downstream can
// correct it.
func TestHTTPSCredentialsToMsg(t *testing.T) {
	t.Parallel()

	basicAuth := func() *model.BasicAuthCredentials {
		return &model.BasicAuthCredentials{
			Username: types.StringValue("telemetry"),
			Password: types.StringValue("secret"),
		}
	}
	authHeaders := func() *model.AuthHeadersCredentials {
		return &model.AuthHeadersCredentials{Headers: map[string]string{"X-Api-Key": "secret"}}
	}

	t.Run("a nil model converts to a nil message", func(t *testing.T) {
		t.Parallel()

		var credentials *model.HTTPSCredentials
		msg, diags := credentials.ToMsg()

		assert.False(t, diags.HasError())
		assert.Nil(t, msg)
	})

	t.Run("basic auth selects the basic_auth arm", func(t *testing.T) {
		t.Parallel()

		credentials := &model.HTTPSCredentials{BasicAuth: basicAuth()}
		msg, diags := credentials.ToMsg()
		require.False(t, diags.HasError())

		require.Equal(t, typesv1beta1.HTTPSCredentials_BasicAuth_case, msg.WhichAuth())
		assert.Equal(t, "telemetry", msg.GetBasicAuth().GetUsername())
		assert.Equal(t, "secret", msg.GetBasicAuth().GetPassword())
	})

	t.Run("auth headers select the auth_headers arm", func(t *testing.T) {
		t.Parallel()

		credentials := &model.HTTPSCredentials{AuthHeaders: authHeaders()}
		msg, diags := credentials.ToMsg()
		require.False(t, diags.HasError())

		require.Equal(t, typesv1beta1.HTTPSCredentials_AuthHeaders_case, msg.WhichAuth())
		assert.Equal(t, map[string]string{"X-Api-Key": "secret"}, msg.GetAuthHeaders().GetHeaders())
	})

	// The headers map is cloned rather than aliased: the message outlives the
	// model and must not change when the model's map does.
	t.Run("the headers map is copied into the message", func(t *testing.T) {
		t.Parallel()

		credentials := &model.HTTPSCredentials{AuthHeaders: authHeaders()}
		msg, diags := credentials.ToMsg()
		require.False(t, diags.HasError())

		credentials.AuthHeaders.Headers["X-Api-Key"] = "rotated"
		assert.Equal(t, "secret", msg.GetAuthHeaders().GetHeaders()["X-Api-Key"])
	})

	t.Run("both auth modes are refused", func(t *testing.T) {
		t.Parallel()

		credentials := &model.HTTPSCredentials{BasicAuth: basicAuth(), AuthHeaders: authHeaders()}
		msg, diags := credentials.ToMsg()

		assert.True(t, diags.HasError())
		assert.Nil(t, msg)
	})

	t.Run("neither auth mode is refused", func(t *testing.T) {
		t.Parallel()

		credentials := &model.HTTPSCredentials{}
		msg, diags := credentials.ToMsg()

		assert.True(t, diags.HasError())
		assert.Nil(t, msg)
	})
}

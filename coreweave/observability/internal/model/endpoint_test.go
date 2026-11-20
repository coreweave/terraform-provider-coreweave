package model_test

import (
	"testing"
	"time"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestForwardingEndpointHTTPSModel pins the HTTPS model's Set/ToMsg mapping.
func TestForwardingEndpointHTTPSModel(t *testing.T) {
	t.Parallel()

	t.Run("round_trip", func(t *testing.T) {
		t.Parallel()

		created := timestamppb.New(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
		updated := timestamppb.New(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))

		endpointMsg := func(mutate func(*typesv1beta1.ForwardingEndpointSpec, *typesv1beta1.ForwardingEndpointStatus)) *typesv1beta1.ForwardingEndpoint {
			spec := &typesv1beta1.ForwardingEndpointSpec{DisplayName: "Test HTTPS Endpoint"}
			spec.SetHttps(&typesv1beta1.HTTPSConfig{Endpoint: testHTTPSEndpoint})

			status := &typesv1beta1.ForwardingEndpointStatus{
				CreatedAt: created,
				UpdatedAt: updated,
				State:     typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED,
			}

			if mutate != nil {
				mutate(spec, status)
			}

			return &typesv1beta1.ForwardingEndpoint{
				Ref:    &typesv1beta1.ForwardingEndpointRef{Slug: "test-https-endpoint"},
				Spec:   spec,
				Status: status,
			}
		}

		t.Run("spec and status populate the model", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(endpointMsg(nil)).HasError())

			assert.Equal(t, "test-https-endpoint", endpoint.Slug.ValueString())
			assert.Equal(t, "Test HTTPS Endpoint", endpoint.DisplayName.ValueString())
			assert.Equal(t, testHTTPSEndpoint, endpoint.Endpoint.ValueString())
			assert.Equal(t, typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String(), endpoint.State.ValueString())
			assert.Equal(t, "2026-10-01T12:00:00Z", endpoint.CreatedAt.ValueString())
			assert.Equal(t, "2026-10-02T12:00:00Z", endpoint.UpdatedAt.ValueString())
		})

		t.Run("an absent state_message reads as null", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(endpointMsg(nil)).HasError())

			assert.True(t, endpoint.StateMessage.IsNull())
		})

		t.Run("an empty state_message is distinct from an absent one", func(t *testing.T) {
			t.Parallel()

			msg := endpointMsg(func(_ *typesv1beta1.ForwardingEndpointSpec, status *typesv1beta1.ForwardingEndpointStatus) {
				status.StateMessage = proto.String("")
			})

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(msg).HasError())

			assert.False(t, endpoint.StateMessage.IsNull())
			assert.Equal(t, "", endpoint.StateMessage.ValueString())
		})

		// UNSPECIFIED is "never configured", which is Terraform's null. NONE is
		// explicitly uncompressed and has to survive the round trip as a value.
		t.Run("unspecified compression reads as null", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(endpointMsg(nil)).HasError())

			assert.True(t, endpoint.Compression.IsNull())

			msg, diags := endpoint.ToMsg()
			require.False(t, diags.HasError())
			assert.Equal(t, typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_UNSPECIFIED, msg.GetSpec().GetHttps().GetCompression())
		})

		t.Run("explicit no compression round-trips as a value", func(t *testing.T) {
			t.Parallel()

			msg := endpointMsg(func(spec *typesv1beta1.ForwardingEndpointSpec, _ *typesv1beta1.ForwardingEndpointStatus) {
				spec.SetHttps(&typesv1beta1.HTTPSConfig{
					Endpoint:    testHTTPSEndpoint,
					Compression: typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_NONE,
				})
			})

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(msg).HasError())
			assert.Equal(t, typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_NONE.String(), endpoint.Compression.ValueString())

			out, diags := endpoint.ToMsg()
			require.False(t, diags.HasError())
			assert.Equal(t, typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_NONE, out.GetSpec().GetHttps().GetCompression())
		})

		// The server never returns credentials, so this pair is the only
		// evidence any were set, and credentials_version must stay untouched
		// because it belongs to the configuration.
		t.Run("credential bookkeeping comes from status alone", func(t *testing.T) {
			t.Parallel()

			msg := endpointMsg(func(_ *typesv1beta1.ForwardingEndpointSpec, status *typesv1beta1.ForwardingEndpointStatus) {
				status.CredentialsConfigured = true
				status.CredentialsUpdatedAt = updated
			})

			endpoint := model.ForwardingEndpointHTTPS{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					CredentialsVersion: types.Int64Value(7),
				},
			}
			require.False(t, endpoint.Set(msg).HasError())

			assert.True(t, endpoint.CredentialsConfigured.ValueBool())
			assert.Equal(t, "2026-10-02T12:00:00Z", endpoint.CredentialsUpdatedAt.ValueString())
			assert.Equal(t, int64(7), endpoint.CredentialsVersion.ValueInt64())
			assert.Nil(t, endpoint.Credentials, "the API never returns credentials")
		})

		t.Run("an endpoint of the wrong config type is refused", func(t *testing.T) {
			t.Parallel()

			spec := &typesv1beta1.ForwardingEndpointSpec{DisplayName: "Test S3 Endpoint"}
			spec.SetS3(&typesv1beta1.S3Config{Uri: "s3://my-telemetry-bucket", Region: "us-east-1"})

			var endpoint model.ForwardingEndpointHTTPS
			diags := endpoint.Set(&typesv1beta1.ForwardingEndpoint{
				Ref:    &typesv1beta1.ForwardingEndpointRef{Slug: "test-s3-endpoint"},
				Spec:   spec,
				Status: &typesv1beta1.ForwardingEndpointStatus{},
			})

			assert.True(t, diags.HasError())
		})

		t.Run("tls survives the round trip", func(t *testing.T) {
			t.Parallel()

			msg := endpointMsg(func(spec *typesv1beta1.ForwardingEndpointSpec, _ *typesv1beta1.ForwardingEndpointStatus) {
				spec.SetHttps(&typesv1beta1.HTTPSConfig{
					Endpoint: testHTTPSEndpoint,
					Tls:      &typesv1beta1.TLSConfig{CertificateAuthorityData: "LS0tLS1CRUdJTi=="},
				})
			})

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(msg).HasError())
			require.NotNil(t, endpoint.TLS)
			assert.Equal(t, "LS0tLS1CRUdJTi==", endpoint.TLS.CertificateAuthorityData.ValueString())

			out, diags := endpoint.ToMsg()
			require.False(t, diags.HasError())
			assert.Equal(t, "LS0tLS1CRUdJTi==", out.GetSpec().GetHttps().GetTls().GetCertificateAuthorityData())
		})

		t.Run("a nil model converts to a nil message", func(t *testing.T) {
			t.Parallel()

			var endpoint *model.ForwardingEndpointHTTPS
			msg, diags := endpoint.ToMsg()

			assert.False(t, diags.HasError())
			assert.Nil(t, msg)
		})
	})
}

// TestForwardingEndpointS3Model pins the S3 model's Set/ToMsg mapping.
func TestForwardingEndpointS3Model(t *testing.T) {
	t.Parallel()

	t.Run("round_trip", func(t *testing.T) {
		t.Parallel()

		created := timestamppb.New(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

		s3Msg := func() *typesv1beta1.ForwardingEndpoint {
			spec := &typesv1beta1.ForwardingEndpointSpec{DisplayName: "Test S3 Endpoint"}
			spec.SetS3(&typesv1beta1.S3Config{
				Uri:    "s3://my-telemetry-bucket/prefix",
				Region: "us-east-1",
			})

			return &typesv1beta1.ForwardingEndpoint{
				Ref:  &typesv1beta1.ForwardingEndpointRef{Slug: "test-s3-endpoint"},
				Spec: spec,
				Status: &typesv1beta1.ForwardingEndpointStatus{
					CreatedAt: created,
					State:     typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED,
				},
			}
		}

		t.Run("spec and status populate the model", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointS3
			require.False(t, endpoint.Set(s3Msg()).HasError())

			assert.Equal(t, "test-s3-endpoint", endpoint.Slug.ValueString())
			assert.Equal(t, "Test S3 Endpoint", endpoint.DisplayName.ValueString())
			assert.Equal(t, "s3://my-telemetry-bucket/prefix", endpoint.URI.ValueString())
			assert.Equal(t, "us-east-1", endpoint.Region.ValueString())
			assert.Equal(t, "2026-10-01T12:00:00Z", endpoint.CreatedAt.ValueString())
			assert.True(t, endpoint.UpdatedAt.IsNull(), "an endpoint never updated has no updated_at")
		})

		t.Run("uri and region survive the round trip", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointS3
			require.False(t, endpoint.Set(s3Msg()).HasError())

			out, diags := endpoint.ToMsg()
			require.False(t, diags.HasError())

			s3 := out.GetSpec().GetS3()
			assert.Equal(t, "s3://my-telemetry-bucket/prefix", s3.GetUri())
			assert.Equal(t, "us-east-1", s3.GetRegion())
		})

		t.Run("an endpoint of the wrong config type is refused", func(t *testing.T) {
			t.Parallel()

			spec := &typesv1beta1.ForwardingEndpointSpec{DisplayName: "Test HTTPS Endpoint"}
			spec.SetHttps(&typesv1beta1.HTTPSConfig{Endpoint: testHTTPSEndpoint})

			var endpoint model.ForwardingEndpointS3
			diags := endpoint.Set(&typesv1beta1.ForwardingEndpoint{
				Ref:    &typesv1beta1.ForwardingEndpointRef{Slug: "test-https-endpoint"},
				Spec:   spec,
				Status: &typesv1beta1.ForwardingEndpointStatus{},
			})

			assert.True(t, diags.HasError())
		})

		// The session token is optional, so an omitted one must not reach the
		// message as an empty string.
		t.Run("an omitted session token is absent from the message", func(t *testing.T) {
			t.Parallel()

			credentials := &model.S3Credentials{
				AccessKeyID:     types.StringValue("AKIAIOSFODNN7EXAMPLE"),
				SecretAccessKey: types.StringValue("secret"),
				SessionToken:    types.StringNull(),
			}

			msg := credentials.ToMsg()
			assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", msg.GetAccessKeyId())
			assert.Empty(t, msg.GetSessionToken())
		})

		t.Run("a set session token reaches the message", func(t *testing.T) {
			t.Parallel()

			credentials := &model.S3Credentials{
				AccessKeyID:     types.StringValue("AKIAIOSFODNN7EXAMPLE"),
				SecretAccessKey: types.StringValue("secret"),
				SessionToken:    types.StringValue("token"),
			}

			assert.Equal(t, "token", credentials.ToMsg().GetSessionToken())
		})

		t.Run("a nil model converts to a nil message", func(t *testing.T) {
			t.Parallel()

			var endpoint *model.ForwardingEndpointS3
			msg, diags := endpoint.ToMsg()

			assert.False(t, diags.HasError())
			assert.Nil(t, msg)
		})
	})
}

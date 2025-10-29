package telecaster_test

import (
	"math/rand/v2"
	"testing"

	"time"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/utils/ptr"
)

func TestForwardingEndpointResourceModel_ToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *telecaster.ForwardingEndpointResourceModel
		expected *typesv1beta1.ForwardingEndpoint
		wantErr  bool
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
			wantErr:  false,
		},
		{
			name: "full input converts correctly",
			input: &telecaster.ForwardingEndpointResourceModel{
				Ref: telecaster.ForwardingEndpointRefModel{
					Slug: types.StringValue("example-endpoint"),
				},
				Spec: telecaster.ForwardingEndpointSpecModel{
					DisplayName: types.StringValue("Test Endpoint"),
					HTTPS: &telecaster.ForwardingEndpointHTTPSModel{
						Endpoint: types.StringValue("https://example.coreweave.com"),
						TLS: &telecaster.TLSConfigModel{
							CertificateAuthorityData: types.StringValue(exampleCA),
						},
						BasicAuth: &telecaster.HTTPSBasicAuthModel{
							Secret: &telecaster.SecretRefModel{
								Slug: types.StringValue("example-secret"),
							},
							UsernameKey: types.StringValue("username"),
							PasswordKey: types.StringValue("password"),
						},
					},
				},
			},
			expected: &typesv1beta1.ForwardingEndpoint{
				Ref: &typesv1beta1.ForwardingEndpointRef{
					Slug: "example-endpoint",
				},
				Spec: &typesv1beta1.ForwardingEndpointSpec{
					DisplayName: "Test Endpoint",
					Config: &typesv1beta1.ForwardingEndpointSpec_Https{
						Https: &typesv1beta1.HTTPSConfig{
							Endpoint: "https://example.coreweave.com",
							Tls: &typesv1beta1.TLSConfig{
								CertificateAuthorityData: exampleCA,
							},
							BasicAuth: &typesv1beta1.HTTPSBasicAuth{
								Secret: &typesv1beta1.SecretRef{
									Slug: "example-secret",
								},
								UsernameKey: "username",
								PasswordKey: "password",
							},
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.input.ToProto()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, got)
			}
		})
	}
}

func TestForwardingEndpointResourceModel_Set(t *testing.T) {
	t.Parallel()

	// Generate two timestamps for CreatedAt and UpdatedAt. UpdatedAt should be after CreatedAt, and both should be in the past.
	// the Terraform RFC3339 implementation ends up comparing using a string representation, and forcing all to UTC avoids a false diff because the timezone expression changes.
	t1 := time.Now().Add(-time.Duration(rand.Int64N(365*24)) * time.Hour).UTC()
	t2 := t1.Add(time.Duration(rand.Int64N(int64(time.Since(t1))))).UTC()

	// note: because there are nested pointers, it's easiest to define these such that they're rebuilt each time.
	baseInput := func() *typesv1beta1.ForwardingEndpoint {
		return &typesv1beta1.ForwardingEndpoint{
			Ref: &typesv1beta1.ForwardingEndpointRef{
				Slug: "example-endpoint",
			},
			Spec: &typesv1beta1.ForwardingEndpointSpec{
				DisplayName: "Test Endpoint",
				Config:      nil, // this must be set by other steps, or else it will panic.
			},
			Status: &typesv1beta1.ForwardingEndpointStatus{
				CreatedAt:    timestamppb.New(t1),
				UpdatedAt:    timestamppb.New(t2),
				State:        typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED,
				StateMessage: ptr.To("Endpoint is connected"),
			},
		}
	}
	baseExpected := func() *telecaster.ForwardingEndpointResourceModel {
		return &telecaster.ForwardingEndpointResourceModel{
			Ref: telecaster.ForwardingEndpointRefModel{
				Slug: types.StringValue("example-endpoint"),
			},
			Spec: telecaster.ForwardingEndpointSpecModel{
				DisplayName: types.StringValue("Test Endpoint"),
				// Config should be set based on input.
			},
			// Status: telecaster.ForwardingEndpointStatusModel{
			// 	CreatedAt:    timetypes.NewRFC3339TimeValue(t1),
			// 	UpdatedAt:    timetypes.NewRFC3339TimeValue(t2),
			// 	State:        types.StringValue(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String()),
			// 	StateCode:    types.Int32Value(int32(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.Number())),
			// 	StateMessage: types.StringValue("Endpoint is connected"),
			// },
		}
	}

	baseHTTPSInput := func() *typesv1beta1.ForwardingEndpointSpec_Https {
		return &typesv1beta1.ForwardingEndpointSpec_Https{
			Https: &typesv1beta1.HTTPSConfig{
				Endpoint: "https://example.coreweave.com",
				Tls: &typesv1beta1.TLSConfig{
					CertificateAuthorityData: exampleCA,
				},
				BasicAuth: &typesv1beta1.HTTPSBasicAuth{
					Secret: &typesv1beta1.SecretRef{
						Slug: "example-secret",
					},
					UsernameKey: "username",
					PasswordKey: "password",
				},
			},
		}
	}
	baseHTTPSExpected := func() *telecaster.ForwardingEndpointHTTPSModel {
		return &telecaster.ForwardingEndpointHTTPSModel{
			Endpoint: types.StringValue("https://example.coreweave.com"),
			TLS: &telecaster.TLSConfigModel{
				CertificateAuthorityData: types.StringValue(exampleCA),
			},
			BasicAuth: &telecaster.HTTPSBasicAuthModel{
				Secret: &telecaster.SecretRefModel{
					Slug: types.StringValue("example-secret"),
				},
				UsernameKey: types.StringValue("username"),
				PasswordKey: types.StringValue("password"),
			},
		}
	}

	baseKafkaInput := func() *typesv1beta1.ForwardingEndpointSpec_Kafka{
		return &typesv1beta1.ForwardingEndpointSpec_Kafka{
			Kafka: &typesv1beta1.KafkaConfig{
				BootstrapEndpoints: "broker1:9092,broker2:9092",
				Topic:              "example-topic",
				Tls: &typesv1beta1.TLSConfig{
					CertificateAuthorityData: exampleCA,
				},
				Auth: &typesv1beta1.KafkaConfig_Scram{
					Scram: &typesv1beta1.KafkaScramAuth{
						Secret: &typesv1beta1.SecretRef{
							Slug: "kafka-secret",
						},
						UsernameKey: "kafka-username",
						PasswordKey: "kafka-password",
					},
				},
			},
		}
	}
	baseKafkaExpected := func() *telecaster.ForwardingEndpointKafkaModel {
		return &telecaster.ForwardingEndpointKafkaModel{
			BootstrapEndpoints: types.StringValue("broker1:9092,broker2:9092"),
			Topic:              types.StringValue("example-topic"),
			TLS: &telecaster.TLSConfigModel{
				CertificateAuthorityData: types.StringValue(exampleCA),
			},
			ScramAuth: &telecaster.KafkaScramAuthModel{
				Secret: &telecaster.SecretRefModel{
					Slug: types.StringValue("kafka-secret"),
				},
				UsernameKey: types.StringValue("kafka-username"),
				PasswordKey: types.StringValue("kafka-password"),
			},
		}
	}

	basePrometheusInput := func() *typesv1beta1.ForwardingEndpointSpec_Prometheus {
		return &typesv1beta1.ForwardingEndpointSpec_Prometheus{
			Prometheus: &typesv1beta1.PrometheusRemoteWriteConfig{
				Endpoint: "http://prometheus.example.com",
				Tls: &typesv1beta1.TLSConfig{
					CertificateAuthorityData: exampleCA,
				},
				BasicAuth: &typesv1beta1.PrometheusBasicAuth{
					Secret: &typesv1beta1.SecretRef{
						Slug: "prometheus-secret",
					},
					UsernameKey: "prometheus-username",
					PasswordKey: "prometheus-password",
				},
			},
		}
	}
	basePrometheusExpected := func() *telecaster.ForwardingEndpointPrometheusModel {
		return &telecaster.ForwardingEndpointPrometheusModel{
			Endpoint: types.StringValue("http://prometheus.example.com"),
			TLS: &telecaster.TLSConfigModel{
				CertificateAuthorityData: types.StringValue(exampleCA),
			},
			BasicAuth: &telecaster.PrometheusBasicAuthModel{
				Secret: &telecaster.SecretRefModel{
					Slug: types.StringValue("prometheus-secret"),
				},
				UsernameKey: types.StringValue("prometheus-username"),
				PasswordKey: types.StringValue("prometheus-password"),
			},
		}
	}

	baseS3Input := func() *typesv1beta1.ForwardingEndpointSpec_S3 {
		return &typesv1beta1.ForwardingEndpointSpec_S3{
			S3: &typesv1beta1.S3Config{
				Uri: "s3://my-bucket/path/",
				Region: "us-north-5",
				Credentials: &typesv1beta1.S3Credentials{
					Secret: &typesv1beta1.SecretRef{
						Slug: "s3-secret",
					},
					AccessKeyIdKey: "AKIAACCESSKEY",
					SecretAccessKeyKey: "RANDOMSECRETKEY",
				},
			},
		}
	}
	baseS3Expected := func() *telecaster.ForwardingEndpointS3Model {
		return &telecaster.ForwardingEndpointS3Model{
			URI:    types.StringValue("s3://my-bucket/path/"),
			Region: types.StringValue("us-north-5"),
			Credentials: &telecaster.S3CredentialsModel{
				Secret: &telecaster.SecretRefModel{
					Slug: types.StringValue("s3-secret"),
				},
				AccessKeyIDKey:     types.StringValue("AKIAACCESSKEY"),
				SecretAccessKeyKey: types.StringValue("RANDOMSECRETKEY"),
			},
		}
	}

	tests := []struct {
		name          string
		startingModel telecaster.ForwardingEndpointResourceModel
		input         *typesv1beta1.ForwardingEndpoint
		expected      *telecaster.ForwardingEndpointResourceModel
	}{
		{
			name: "full https input converts correctly",
			input: with(baseInput(), func(fe *typesv1beta1.ForwardingEndpoint) {
				fe.Spec.Config = baseHTTPSInput()
			}),
			expected: with(baseExpected(), func(model *telecaster.ForwardingEndpointResourceModel) {
				model.Spec.HTTPS = baseHTTPSExpected()
			}),
		},
		{
			name: "full kafka input converts correctly",
			input: with(baseInput(), func(fe *typesv1beta1.ForwardingEndpoint) {
				fe.Spec.Config = baseKafkaInput()
			}),
			expected: with(baseExpected(), func(model *telecaster.ForwardingEndpointResourceModel) {
				model.Spec.Kafka = baseKafkaExpected()
			}),
		},
		{
			name: "full Prometheus input converts correctly",
			input: with(baseInput(), func(fe *typesv1beta1.ForwardingEndpoint) {
				fe.Spec.Config = basePrometheusInput()
			}),
			expected: with(baseExpected(), func(model *telecaster.ForwardingEndpointResourceModel) {
				model.Spec.Prometheus = basePrometheusExpected()
			}),
		},
		{
			name: "full S3 input converts correctly",
			input: with(baseInput(), func(fe *typesv1beta1.ForwardingEndpoint) {
				fe.Spec.Config = baseS3Input()
			}),
			expected: with(baseExpected(), func(model *telecaster.ForwardingEndpointResourceModel) {
				model.Spec.S3 = baseS3Expected()
			}),
		},
		{
			name:     "changed config overwrites existing",
			startingModel: *with(baseExpected(), func(model *telecaster.ForwardingEndpointResourceModel) {
				model.Spec.HTTPS = baseHTTPSExpected()
			}),
			input:    with(baseInput(), func(fe *typesv1beta1.ForwardingEndpoint) {
				fe.Spec.Config = basePrometheusInput()
			}),
			expected: with(baseExpected(), func(model *telecaster.ForwardingEndpointResourceModel) {
				model.Spec.Prometheus = basePrometheusExpected()
			}),
		},
		{
			name: "error status propagates correctly",
			input: with(baseInput(), func(fe *typesv1beta1.ForwardingEndpoint) {
				fe.Spec.Config = baseKafkaInput()
				fe.Status.State = typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_ERROR
				fe.Status.StateMessage = ptr.To("There was an error connecting to the endpoint")
			}),
			// expected: with(baseExpected(), func(model *telecaster.ForwardingEndpointResourceModel) {
			// 	model.Spec.Kafka = baseKafkaExpected()
			// 	model.Status.State = types.StringValue(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_ERROR.String())
			// 	model.Status.StateCode = types.Int32Value(int32(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_ERROR.Number()))
			// 	model.Status.StateMessage = types.StringValue("There was an error connecting to the endpoint")
			// }),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := &tt.startingModel
			model.Set(tt.input)
			assert.Equal(t, tt.expected, model)
		})
	}
}

func TestForwardingEndpointSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	schemaRequest := resource.SchemaRequest{}
	schemaResponse := &resource.SchemaResponse{}

	telecaster.NewForwardingEndpointResource().Schema(ctx, schemaRequest, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}

package telecaster_test

import (
	"testing"

	telecastertypesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/cw/telecaster/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
)

const (
	exampleCA = "NVJDOXBOME5sY0RoeFYyNUJLemRCUlM4egpRak5UTHpOa1JVVlpiV013YkhCbE1UTTJOa0V2TmtkRloyc3phM1J5T1ZCRmIxRnlURU5vY3paSkNuUjFNM2R1VGt4Q01tVjEKUXpoSlMwZE1VVVp3UjNSUFR5OHlMMmhwUVV0cWVXRnFZVUpRTWpWM01XcEdNRmRzT0VKaWNXNWxNM1ZhTW5FeFIzbFFSa29LCldWSnRWRGN2VDFod2JVOUlMMFpXVEhSM1V5czRibWN4WTBGdGNFTjFhbEIzZEdWS1drNWpSRWN3YzBZeWJpOXpZekFyVTFGbQpORGxtWkhsVlN6QjBlUW9yVmxWM1JtbzVkRzFYZUhsU0wwMDlDaTB0TFMwdFJVNUVJRU5GVWxSSlJrbERRVlJGTFMwdExTMD0="
)

func TestForwardingEndpointResourceModel_ToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *telecaster.ForwardingEndpointResourceModel
		expected *telecastertypesv1beta1.ForwardingEndpoint
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
			expected: &telecastertypesv1beta1.ForwardingEndpoint{
				Ref: &telecastertypesv1beta1.ForwardingEndpointRef{
					Slug: "example-endpoint",
				},
				Spec: &telecastertypesv1beta1.ForwardingEndpointSpec{
					DisplayName: "Test Endpoint",
					Config: &telecastertypesv1beta1.ForwardingEndpointSpec_Https{
						Https: &telecastertypesv1beta1.HTTPSConfig{
							Endpoint: "https://example.coreweave.com",
							Tls: &telecastertypesv1beta1.TLSConfig{
								CertificateAuthorityData: exampleCA,
							},
							BasicAuth: &telecastertypesv1beta1.HTTPSBasicAuth{
								Secret: &telecastertypesv1beta1.SecretRef{
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

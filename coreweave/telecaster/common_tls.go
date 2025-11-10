package telecaster

import (
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type TLSConfigModel struct {
	CertificateAuthorityData types.String `tfsdk:"certificate_authority_data"`
}

func (t *TLSConfigModel) Set(tlsConfig *typesv1beta1.TLSConfig) (diagnostics diag.Diagnostics) {
	t.CertificateAuthorityData = types.StringValue(tlsConfig.CertificateAuthorityData)
	return
}

func (t *TLSConfigModel) ToProto() *typesv1beta1.TLSConfig {
	if t == nil {
		return nil
	}

	return &typesv1beta1.TLSConfig{
		CertificateAuthorityData: t.CertificateAuthorityData.ValueString(),
	}
}

func tlsConfigModelAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "Configuration for TLS connections.",
		Attributes: map[string]schema.Attribute{
			"certificate_authority_data": schema.StringAttribute{
				MarkdownDescription: "Base64 encoded CA certificate data.",
				Required:            true,
			},
		},
		Optional: true,
	}
}

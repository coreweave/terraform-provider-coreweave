package model

import (
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type TLSConfig struct {
	CertificateAuthorityData types.String `tfsdk:"certificate_authority_data"`
}

func (t *TLSConfig) Set(tlsConfig *typesv1beta1.TLSConfig) {
	t.CertificateAuthorityData = types.StringValue(tlsConfig.CertificateAuthorityData)
}

func (t *TLSConfig) ToMsg() *typesv1beta1.TLSConfig {
	if t == nil {
		return nil
	}

	return &typesv1beta1.TLSConfig{
		CertificateAuthorityData: t.CertificateAuthorityData.ValueString(),
	}
}

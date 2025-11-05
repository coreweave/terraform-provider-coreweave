module github.com/coreweave/terraform-provider-coreweave

go 1.25.2

require (
	bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/connectrpc/go v1.18.1-20251110050257-02cdc61e2538.1
	bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go v1.36.4-20251104003945-69ba9a1af7d0.1
	buf.build/gen/go/coreweave/cks/connectrpc/go v1.19.0-20251007230423-8a2391dbf182.1
	buf.build/gen/go/coreweave/cks/protocolbuffers/go v1.36.10-20251007230423-8a2391dbf182.1
	buf.build/gen/go/coreweave/cwobject/connectrpc/go v1.19.1-20250604181649-b97f17b05d5b.2
	buf.build/gen/go/coreweave/cwobject/protocolbuffers/go v1.36.10-20250604181649-b97f17b05d5b.1
	buf.build/gen/go/coreweave/networking/connectrpc/go v1.19.1-20251112175914-2bd1efefa63c.2
	buf.build/gen/go/coreweave/networking/protocolbuffers/go v1.36.10-20251112175914-2bd1efefa63c.1
	connectrpc.com/connect v1.19.1
	github.com/aws/aws-sdk-go-v2 v1.37.0
	github.com/aws/aws-sdk-go-v2/config v1.30.1
	github.com/aws/aws-sdk-go-v2/credentials v1.18.1
	github.com/aws/aws-sdk-go-v2/service/s3 v1.85.0
	github.com/aws/smithy-go v1.22.5
	github.com/hashicorp/go-cleanhttp v0.5.2
	github.com/hashicorp/go-retryablehttp v0.7.8
	github.com/hashicorp/go-uuid v1.0.3
	github.com/hashicorp/hcl/v2 v2.23.0
	github.com/hashicorp/terraform-plugin-framework v1.15.1
	github.com/hashicorp/terraform-plugin-framework-timetypes v0.5.0
	github.com/hashicorp/terraform-plugin-framework-validators v0.18.0
	github.com/hashicorp/terraform-plugin-go v0.27.0
	github.com/hashicorp/terraform-plugin-log v0.9.0
	github.com/hashicorp/terraform-plugin-sdk/v2 v2.35.0
	github.com/hashicorp/terraform-plugin-testing v1.11.0
	github.com/stretchr/testify v1.11.1
	github.com/zclconf/go-cty v1.15.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250908214217-97024824d090
	google.golang.org/protobuf v1.36.10
	k8s.io/utils v0.0.0-20251002143259-bc988d571ff4
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.10-20250912141014-52f32327d4b0.1 // indirect
	buf.build/gen/go/grpc-ecosystem/grpc-gateway/protocolbuffers/go v1.36.10-20241220201140-4c5ba75caaf8.1 // indirect
	dario.cat/mergo v1.0.2 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ProtonMail/go-crypto v1.1.0-alpha.2 // indirect
	github.com/agext/levenshtein v1.2.2 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.0 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.0 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.0 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.0 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.8.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.26.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.31.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.35.0 // indirect
	github.com/cloudflare/circl v1.3.7 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-checkpoint v0.5.0 // indirect
	github.com/hashicorp/go-cty v1.4.1-0.20200414143053-d3edf31b6320 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-plugin v1.6.3 // indirect
	github.com/hashicorp/go-version v1.7.0 // indirect
	github.com/hashicorp/hc-install v0.9.0 // indirect
	github.com/hashicorp/logutils v1.0.0 // indirect
	github.com/hashicorp/terraform-exec v0.21.0 // indirect
	github.com/hashicorp/terraform-json v0.23.0 // indirect
	github.com/hashicorp/terraform-registry-address v0.2.5 // indirect
	github.com/hashicorp/terraform-svchost v0.1.1 // indirect
	github.com/hashicorp/yamux v0.1.1 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-testing-interface v1.14.1 // indirect
	github.com/mitchellh/go-wordwrap v1.0.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/oklog/run v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/vmihailenco/msgpack v4.0.4+incompatible // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.38.0 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/mod v0.27.0 // indirect
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	golang.org/x/tools v0.36.0 // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250908214217-97024824d090 // indirect
	google.golang.org/grpc v1.72.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

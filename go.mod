module github.com/coreweave/terraform-provider-coreweave

go 1.23.0

toolchain go1.24.0

require (
	buf.build/gen/go/coreweave/cks/connectrpc/go v1.18.1-20250310185152-f420fd79513c.1
	buf.build/gen/go/coreweave/cks/protocolbuffers/go v1.36.5-20250310185152-f420fd79513c.1
	buf.build/gen/go/coreweave/cwobject/connectrpc/go v1.18.1-20250604181649-b97f17b05d5b.1
	buf.build/gen/go/coreweave/cwobject/protocolbuffers/go v1.36.2-20250604181649-b97f17b05d5b.1
	buf.build/gen/go/coreweave/networking/connectrpc/go v1.18.1-20250310211902-b930cb89bad3.1
	buf.build/gen/go/coreweave/networking/protocolbuffers/go v1.36.2-20250310211902-b930cb89bad3.1
	connectrpc.com/connect v1.18.1
	github.com/aws/aws-sdk-go-v2 v1.37.0
	github.com/aws/aws-sdk-go-v2/config v1.30.1
	github.com/aws/aws-sdk-go-v2/credentials v1.18.1
	github.com/aws/aws-sdk-go-v2/service/s3 v1.85.0
	github.com/aws/smithy-go v1.22.5
	github.com/hashicorp/go-cleanhttp v0.5.2
	github.com/hashicorp/go-retryablehttp v0.7.7
	github.com/hashicorp/hcl/v2 v2.23.0
	github.com/hashicorp/terraform-plugin-framework v1.15.1
	github.com/hashicorp/terraform-plugin-go v0.27.0
	github.com/hashicorp/terraform-plugin-log v0.9.0
	github.com/hashicorp/terraform-plugin-sdk/v2 v2.35.0
	github.com/hashicorp/terraform-plugin-testing v1.11.0
	github.com/zclconf/go-cty v1.17.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250218202821-56aae31c358a
	google.golang.org/protobuf v1.36.6
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.5-20250219170025-d39267d9df8f.1 // indirect
	buf.build/gen/go/grpc-ecosystem/grpc-gateway/protocolbuffers/go v1.36.2-20241220201140-4c5ba75caaf8.1 // indirect
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
	github.com/fatih/color v1.16.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-checkpoint v0.5.0 // indirect
	github.com/hashicorp/go-cty v1.4.1-0.20200414143053-d3edf31b6320 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-plugin v1.6.3 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/go-version v1.7.0 // indirect
	github.com/hashicorp/hc-install v0.9.0 // indirect
	github.com/hashicorp/logutils v1.0.0 // indirect
	github.com/hashicorp/terraform-exec v0.21.0 // indirect
	github.com/hashicorp/terraform-json v0.23.0 // indirect
	github.com/hashicorp/terraform-registry-address v0.2.5 // indirect
	github.com/hashicorp/terraform-svchost v0.1.1 // indirect
	github.com/hashicorp/yamux v0.1.1 // indirect
	github.com/kr/pretty v0.3.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-testing-interface v1.14.1 // indirect
	github.com/mitchellh/go-wordwrap v1.0.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/oklog/run v1.0.0 // indirect
	github.com/rogpeppe/go-internal v1.12.0 // indirect
	github.com/vmihailenco/msgpack v4.0.4+incompatible // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/mod v0.21.0 // indirect
	golang.org/x/net v0.39.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	golang.org/x/tools v0.21.1-0.20240508182429-e35e4ccd0d2d // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250218202821-56aae31c358a // indirect
	google.golang.org/grpc v1.72.1 // indirect
)

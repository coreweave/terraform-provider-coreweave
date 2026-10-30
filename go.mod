module github.com/coreweave/terraform-provider-coreweave

go 1.27.2

require (
	buf.build/gen/go/coreweave/cks/connectrpc/go/v2 v2.0.2-20261014193236-69d4f39faca7.1
	buf.build/gen/go/coreweave/cks/protocolbuffers/go v1.36.12-20261014193236-69d4f39faca7.2
	buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/v2 v2.0.1-20261009031350-4d1490e50ed8.1
	buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go v1.36.12-20261009031350-4d1490e50ed8.2
	buf.build/gen/go/coreweave/cwobject/connectrpc/go/v2 v2.0.0-20260831154917-08064e55742b.1
	buf.build/gen/go/coreweave/cwobject/protocolbuffers/go v1.36.12-20260831154917-08064e55742b.2
	buf.build/gen/go/coreweave/directory/connectrpc/go/v2 v2.0.1-20261022011331-2af0c9345d0d.1
	buf.build/gen/go/coreweave/directory/protocolbuffers/go v1.36.12-20261022011331-2af0c9345d0d.2
	buf.build/gen/go/coreweave/inference/connectrpc/go/v2 v2.0.0-20261008185853-22f8f31f8962.1
	buf.build/gen/go/coreweave/inference/protocolbuffers/go v1.36.12-20261008185853-22f8f31f8962.2
	buf.build/gen/go/coreweave/networking/connectrpc/go/v2 v2.0.2-20261021202238-84a5f410acc9.1
	buf.build/gen/go/coreweave/networking/protocolbuffers/go v1.36.12-20261021202238-84a5f410acc9.2
	buf.build/gen/go/coreweave/sandbox/connectrpc/go/v2 v2.0.1-20261015202918-7074ae5be483.1
	buf.build/gen/go/coreweave/sandbox/protocolbuffers/go v1.36.12-20261015202918-7074ae5be483.2
	buf.build/gen/go/coreweave/workload-federation/connectrpc/go/v2 v2.0.0-20261013191007-c36df30df3ed.1
	buf.build/gen/go/coreweave/workload-federation/protocolbuffers/go v1.36.12-20261013191007-c36df30df3ed.2
	buf.build/go/protovalidate v1.4.0
	cloud.google.com/go/longrunning v1.3.0
	connectrpc.com/connect/v2 v2.0.2
	github.com/aws/aws-sdk-go-v2 v1.47.3
	github.com/aws/aws-sdk-go-v2/credentials v1.20.9
	github.com/aws/aws-sdk-go-v2/service/s3 v1.114.3
	github.com/aws/smithy-go v1.28.5
	github.com/hashicorp/go-cleanhttp v0.5.2
	github.com/hashicorp/go-retryablehttp v0.7.8
	github.com/hashicorp/go-uuid v1.0.4
	github.com/hashicorp/hcl/v2 v2.25.0
	github.com/hashicorp/terraform-plugin-framework v1.19.0
	github.com/hashicorp/terraform-plugin-framework-jsontypes v0.2.0
	github.com/hashicorp/terraform-plugin-framework-nettypes v0.3.0
	github.com/hashicorp/terraform-plugin-framework-timeouts v0.7.0
	github.com/hashicorp/terraform-plugin-framework-validators v0.19.0
	github.com/hashicorp/terraform-plugin-go v0.31.0
	github.com/hashicorp/terraform-plugin-log v0.11.0
	github.com/hashicorp/terraform-plugin-sdk/v2 v2.40.1
	github.com/hashicorp/terraform-plugin-testing v1.16.0
	github.com/stretchr/testify v1.12.1
	github.com/zclconf/go-cty v1.19.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260819154853-08b0e4226688
	google.golang.org/protobuf v1.36.12
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2 // indirect
	buf.build/gen/go/gnostic/gnostic/protocolbuffers/go v1.36.12-20230414000709-087bc8072ce4.2 // indirect
	buf.build/gen/go/grpc-ecosystem/grpc-gateway/protocolbuffers/go v1.36.12-20260928204428-da388df5b314.2 // indirect
	cel.dev/cel-go v0.32.0 // indirect
	cel.dev/expr v0.25.3 // indirect
	github.com/ProtonMail/go-crypto v1.4.1 // indirect
	github.com/agext/levenshtein v1.2.3 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/apparentlymart/go-textseg/v17 v17.0.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.22 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.6 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-checkpoint v0.5.0 // indirect
	github.com/hashicorp/go-cty v1.5.0 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-plugin v1.7.0 // indirect
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/hashicorp/hc-install v0.9.4 // indirect
	github.com/hashicorp/logutils v1.0.0 // indirect
	github.com/hashicorp/terraform-exec v0.25.1 // indirect
	github.com/hashicorp/terraform-json v0.27.2 // indirect
	github.com/hashicorp/terraform-registry-address v0.4.0 // indirect
	github.com/hashicorp/terraform-svchost v0.2.1 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-testing-interface v1.14.1 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/oklog/run v1.2.0 // indirect
	github.com/vmihailenco/msgpack v4.0.4+incompatible // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/exp v0.0.0-20260820142414-ca536658362e // indirect
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/grpc v1.83.2 // indirect
)

package observability

import (
	"context"
	"fmt"
	"strings"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
)

var (
	_ datasource.DataSourceWithConfigure      = &TelemetryStreamDataSource{}
	_ datasource.DataSourceWithValidateConfig = &TelemetryStreamDataSource{}

	streamSpecKinds = []string{
		typesv1beta1.TelemetryStreamSpec_Kind_not_set_case.String(),
		typesv1beta1.TelemetryStreamSpec_Metrics_case.String(),
		typesv1beta1.TelemetryStreamSpec_Logs_case.String(),
	}
)

func NewTelemetryStreamDataSource() datasource.DataSource {
	return &TelemetryStreamDataSource{}
}

type TelemetryStreamDataSource struct {
	coretf.CoreDataSource
}

func (s *TelemetryStreamDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var data model.TelemetryStreamDataSource

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	msg := &clusterv1beta1.GetStreamRequest{
		Ref: data.ToRef(),
	}

	if err := protovalidate.Validate(msg); err != nil {
		resp.Diagnostics.AddError("Validation Error", err.Error())
	}
}

func (s *TelemetryStreamDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_observability_telemetry_relay_stream"
}

func (s *TelemetryStreamDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telemetry Relay stream data source. Read telemetry stream configuration and status.",
		Attributes: map[string]schema.Attribute{
			// Ref fields
			"slug": schema.StringAttribute{
				MarkdownDescription: "The slug of the stream. Used as a unique identifier.",
				Required:            true,
			},
			// Spec fields
			"display_name": schema.StringAttribute{
				MarkdownDescription: "The display name of the stream.",
				Computed:            true,
			},
			"kind": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("The kind of the stream (one of: %s)", strings.Join(streamSpecKinds, ", ")),
				Computed:            true,
			},
			"logs": schema.SingleNestedAttribute{
				MarkdownDescription: "Logs stream configuration, if it is a logs stream.",
				Computed:            true,
				Attributes:          map[string]schema.Attribute{},
			},
			"metrics": schema.SingleNestedAttribute{
				MarkdownDescription: "Metrics stream configuration, if it is a metrics stream.",
				Computed:            true,
				Attributes:          map[string]schema.Attribute{},
			},
			// Status fields
			"created_at": schema.StringAttribute{
				MarkdownDescription: "The time the stream was created.",
				Computed:            true,
				CustomType:          timetypes.RFC3339Type{},
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "The time the stream was last updated.",
				Computed:            true,
				CustomType:          timetypes.RFC3339Type{},
			},
			"state_code": schema.Int32Attribute{
				MarkdownDescription: "The numeric state code of the stream.",
				Computed:            true,
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "The string representation of the stream state.",
				Computed:            true,
			},
			"state_message": schema.StringAttribute{
				MarkdownDescription: "Additional information about the current stream state.",
				Computed:            true,
			},
		},
	}
}

func (s *TelemetryStreamDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data model.TelemetryStreamDataSource

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getReq := &clusterv1beta1.GetStreamRequest{
		Ref: data.ToRef(),
	}

	getResp, err := s.Client.GetStream(ctx, connect.NewRequest(getReq))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.Diagnostics.AddError(
				"Stream Not Found",
				fmt.Sprintf("The specified stream with slug %q was not found.", getReq.Ref.Slug),
			)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(data.Set(getResp.Msg.Stream)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

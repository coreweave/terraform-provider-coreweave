package telecaster

import (
	"context"
	"fmt"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"connectrpc.com/connect"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSourceWithConfigure = &TelemetryStreamDataSource{}
)

func NewTelemetryStreamDataSource() datasource.DataSource {
	return &TelemetryStreamDataSource{}
}

type TelemetryStreamDataSource struct {
	coretf.CoreDataSource
}

type TelemetryStreamDataSourceModel struct {
	Ref    TelemetryStreamRefModel `tfsdk:"ref"`
	Spec   types.Object            `tfsdk:"spec"`
	Status types.Object            `tfsdk:"status"`
}

func (s *TelemetryStreamDataSourceModel) Set(ctx context.Context, stream *typesv1beta1.TelemetryStream) (diagnostics diag.Diagnostics) {
	s.Ref = TelemetryStreamRefModel{Slug: types.StringValue(stream.Ref.Slug)}

	var spec TelemetryStreamSpecModel
	spec.DisplayName = types.StringValue(stream.Spec.DisplayName)
	switch stream.Spec.Kind.(type) {
	case *typesv1beta1.TelemetryStreamSpec_Metrics:
		spec.Metrics = &MetricsStreamSpecModel{}
	case *typesv1beta1.TelemetryStreamSpec_Logs:
		spec.Logs = &LogsStreamSpecModel{}
	default:
		// no kind set
	}
	specModel, errs := types.ObjectValueFrom(ctx, s.Spec.AttributeTypes(ctx), &spec)
	diagnostics.Append(errs...)
	s.Spec = specModel

	var status TelemetryStreamStatusModel
	status.Set(stream.Status)
	statusModel, errs := types.ObjectValueFrom(ctx, s.Status.AttributeTypes(ctx), &status)
	diagnostics.Append(errs...)
	s.Status = statusModel

	return
}

type TelemetryStreamStatusModel struct {
	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	StateString  types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

func (s *TelemetryStreamStatusModel) Set(status *typesv1beta1.TelemetryStreamStatus) {
	s.CreatedAt = timetypes.NewRFC3339TimeValue(status.CreatedAt.AsTime())
	s.UpdatedAt = timetypes.NewRFC3339TimeValue(status.UpdatedAt.AsTime())
	s.StateCode = types.Int32Value(int32(status.State.Number()))
	s.StateString = types.StringValue(status.State.String())
	s.StateMessage = types.StringPointerValue(status.StateMessage)
}

func (s *TelemetryStreamDataSourceModel) toGetRequest() *clusterv1beta1.GetStreamRequest {
	return &clusterv1beta1.GetStreamRequest{Ref: s.Ref.toProtoObject()}
}

type TelemetryStreamRefModel struct {
	Slug types.String `tfsdk:"slug"`
}

func (s *TelemetryStreamRefModel) toProtoObject() *typesv1beta1.TelemetryStreamRef {
	return &typesv1beta1.TelemetryStreamRef{Slug: s.Slug.ValueString()}
}

type TelemetryStreamSpecModel struct {
	DisplayName types.String            `tfsdk:"display_name"`
	Logs        *LogsStreamSpecModel    `tfsdk:"logs"`
	Metrics     *MetricsStreamSpecModel `tfsdk:"metrics"`
}

type LogsStreamSpecModel struct {
}

type MetricsStreamSpecModel struct {
}

func (s *TelemetryStreamDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telecaster_stream"
}

func (s *TelemetryStreamDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telecaster stream data source",
		Attributes: map[string]schema.Attribute{
			"ref": schema.SingleNestedAttribute{
				MarkdownDescription: "Reference to the Telecaster stream.",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"slug": schema.StringAttribute{
						MarkdownDescription: "The slug of the stream. Used as a unique identifier.",
						Required:            true,
					},
				},
			},
			"spec": schema.SingleNestedAttribute{
				MarkdownDescription: "The specification for the stream.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"display_name": schema.StringAttribute{
						MarkdownDescription: "The display name of the stream.",
						Computed:            true,
					},
					"logs": schema.SingleNestedAttribute{
						MarkdownDescription: "Logs stream configuration.",
						Computed:            true,
						Attributes:          map[string]schema.Attribute{},
					},
					"metrics": schema.SingleNestedAttribute{
						MarkdownDescription: "Metrics stream configuration.",
						Computed:            true,
						Attributes:          map[string]schema.Attribute{},
					},
				},
			},
			"status": schema.SingleNestedAttribute{
				MarkdownDescription: "The status of the stream.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
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
			},
		},
	}
}

func (s *TelemetryStreamDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TelemetryStreamDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := s.Client.GetStream(ctx, connect.NewRequest(data.toGetRequest()))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.Diagnostics.AddWarning(
				"Stream Not Found",
				fmt.Sprintf("The specified stream with slug '%s' was not found.", data.Ref.Slug.ValueString()),
			)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(data.Set(ctx, getResp.Msg.Stream)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

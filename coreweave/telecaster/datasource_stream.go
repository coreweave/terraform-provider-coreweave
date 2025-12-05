package telecaster

import (
	"context"
	"fmt"
	"strings"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
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

type TelemetryStreamDataSourceSpecModel struct {
	model.TelemetryStreamSpecModel
	// TODO: Use value from API
	Kind types.String `tfsdk:"kind"`
}

func (s *TelemetryStreamDataSourceSpecModel) Set(spec *typesv1beta1.TelemetryStreamSpec) (diagnostics diag.Diagnostics) {
	diagnostics.Append(s.TelemetryStreamSpecModel.Set(spec)...)
	s.Kind = types.StringValue(spec.WhichKind().String())
	return
}

type TelemetryStreamDataSourceModel struct {
	Ref    types.Object `tfsdk:"ref"`
	Spec   types.Object `tfsdk:"spec"`
	Status types.Object `tfsdk:"status"`
}

func (s *TelemetryStreamDataSourceModel) Set(ctx context.Context, stream *typesv1beta1.TelemetryStream) (diagnostics diag.Diagnostics) {
	var ref model.TelemetryStreamRefModel
	// .As() hydrates information from the plan and schema into the model before we set it. Unhandled nulls/unknowns are not acceptable for refs.
	diagnostics.Append(s.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	ref.Set(stream.Ref)
	refObj, diags := types.ObjectValueFrom(ctx, s.Ref.AttributeTypes(ctx), &ref)
	diagnostics.Append(diags...)
	s.Ref = refObj

	var spec TelemetryStreamDataSourceSpecModel
	// Hydrate like ref, but allow unhandled nulls/unknowns since this is not expected be known before we read.
	diagnostics.Append(s.Spec.As(ctx, &spec, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true})...)
	spec.Set(stream.Spec)
	specObj, specDiags := types.ObjectValueFrom(ctx, s.Spec.AttributeTypes(ctx), &spec)
	diagnostics.Append(specDiags...)
	s.Spec = specObj

	var status model.TelemetryStreamStatusModel
	diagnostics.Append(s.Status.As(ctx, &status, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true})...)
	status.Set(stream.Status)
	statusObj, statusDiags := types.ObjectValueFrom(ctx, s.Status.AttributeTypes(ctx), &status)
	diagnostics.Append(statusDiags...)
	s.Status = statusObj

	return
}

func (s *TelemetryStreamDataSourceModel) toGetRequest(ctx context.Context) (msg *clusterv1beta1.GetStreamRequest, diagnostics diag.Diagnostics) {
	var ref model.TelemetryStreamRefModel
	diagnostics.Append(s.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	if diagnostics.HasError() {
		return
	}
	refMsg := ref.ToMsg()

	if diagnostics.HasError() {
		return
	}

	msg = &clusterv1beta1.GetStreamRequest{Ref: refMsg}

	return
}

func (s *TelemetryStreamDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var data TelemetryStreamDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	msg, diags := data.toGetRequest(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := protovalidate.Validate(msg); err != nil {
		resp.Diagnostics.AddError("Validation Error", err.Error())
	}
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
					"kind": schema.StringAttribute{
						MarkdownDescription: fmt.Sprintf("The kind of the stream (one of: %s)", strings.Join(streamSpecKinds, ", ")),
						Computed:            true,
					},
					"logs": schema.SingleNestedAttribute{
						MarkdownDescription: "Logs stream configuration, if it is a logs stream.",
						Computed:            true,
						Optional:            true,
						Attributes:          map[string]schema.Attribute{},
					},
					"metrics": schema.SingleNestedAttribute{
						MarkdownDescription: "Metrics stream configuration, if it is a metrics stream.",
						Optional:            true,
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

	getReq, diags := data.toGetRequest(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := s.Client.GetStream(ctx, connect.NewRequest(getReq))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.Diagnostics.AddWarning(
				"Stream Not Found",
				fmt.Sprintf("The specified stream with slug %q was not found.", getReq.Ref.Slug),
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

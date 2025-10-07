package telecaster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	clusterv1beta1 "github.com/coreweave/o11y-mgmt/gen/cw/telecaster/svc/cluster/v1beta1"
	telecastertypesv1beta1 "github.com/coreweave/o11y-mgmt/gen/cw/telecaster/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

var (
	_ resource.Resource = &StreamResource{}
)

func NewStreamResource() resource.Resource {
	return &StreamResource{}
}

type StreamResource struct {
	client *coreweave.Client
}

type StreamResourceModel struct {
	StreamRefModel

	Spec *StreamSpecModel `tfsdk:"spec"`
}

func (s *StreamResourceModel) Set(stream *telecastertypesv1beta1.TelemetryStream) {
	s.StreamRefModel = StreamRefModel{Slug: types.StringValue(stream.Ref.Slug)}
	s.Spec = &StreamSpecModel{
		DisplayName: types.StringValue(stream.Spec.DisplayName),
	}

	switch stream.Spec.Kind.(type) {
	case *telecastertypesv1beta1.TelemetryStreamSpec_Metrics:
		s.Spec.Metrics = &MetricsStreamSpecModel{}
	case *telecastertypesv1beta1.TelemetryStreamSpec_Logs:
		s.Spec.Logs = &LogsStreamSpecModel{}
	default:
		panic(fmt.Sprintf("cannot set StreamResourceModel; unknown stream kind: %T", stream.Spec.Kind))
	}
}

func (s *StreamResourceModel) toCreateRequest() (*clusterv1beta1.CreateStreamRequest, error) {
	stream, err := s.toProtoObject()
	if err != nil {
		return nil, err
	}
	req := &clusterv1beta1.CreateStreamRequest{Stream: stream}
	return req, nil
}

func (s *StreamResourceModel) toGetRequest() *clusterv1beta1.GetStreamRequest {
	ref := s.StreamRefModel.toProtoObject()
	return &clusterv1beta1.GetStreamRequest{Ref: ref}
}

func (s *StreamResourceModel) toUpdateRequest() (*clusterv1beta1.UpdateStreamRequest, error) {
	stream, err := s.toProtoObject()
	if err != nil {
		return nil, err
	}
	req := &clusterv1beta1.UpdateStreamRequest{Ref: stream.Ref, Spec: stream.Spec}
	return req, nil
}

func (s *StreamResourceModel) toDeleteRequest() *clusterv1beta1.DeleteStreamRequest {
	ref := s.StreamRefModel.toProtoObject()
	return &clusterv1beta1.DeleteStreamRequest{Ref: ref}
}

type StreamRefModel struct {
	Slug types.String `tfsdk:"slug"`
}

func (s *StreamRefModel) toProtoObject() *telecastertypesv1beta1.TelemetryStreamRef {
	return &telecastertypesv1beta1.TelemetryStreamRef{Slug: s.Slug.ValueString()}
}

type StreamSpecModel struct {
	DisplayName types.String            `tfsdk:"display_name"`
	Logs        *LogsStreamSpecModel    `tfsdk:"logs"`
	Metrics     *MetricsStreamSpecModel `tfsdk:"metrics"`
}

func (s *StreamSpecModel) toProtoObject() (*telecastertypesv1beta1.TelemetryStreamSpec, error) {
	if s.Logs == nil && s.Metrics == nil {
		return nil, errors.New("either logs or metrics must be specified")
	}
	if s.Logs != nil && s.Metrics != nil {
		return nil, errors.New("only one of logs or metrics must be specified")
	}

	// because the implementation does not provide a usable interface here, we have to create the object then assign the logs/metrics field using a concrete type.
	result := &telecastertypesv1beta1.TelemetryStreamSpec{
		DisplayName: s.DisplayName.ValueString(),
	}
	if s.Metrics != nil {
		result.Kind = &telecastertypesv1beta1.TelemetryStreamSpec_Metrics{}
	} else if s.Logs != nil {
		result.Kind = &telecastertypesv1beta1.TelemetryStreamSpec_Logs{}
	}
	return result, nil
}

type LogsStreamSpecModel struct {
}

type MetricsStreamSpecModel struct {
}

func (s *StreamResourceModel) toProtoObject() (*telecastertypesv1beta1.TelemetryStream, error) {
	ref := s.StreamRefModel.toProtoObject()
	spec, err := s.Spec.toProtoObject()
	if err != nil {
		return nil, err
	}

	stream := &telecastertypesv1beta1.TelemetryStream{
		Ref:  ref,
		Spec: spec,
	}
	return stream, nil
}

func (s *StreamResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telecaster_stream"
}

func (s *StreamResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *coreweave.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	s.client = client
}

func (s *StreamResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telecaster Stream",
		Attributes: map[string]schema.Attribute{
			"slug": schema.StringAttribute{
				MarkdownDescription: "The slug of the stream. Used as a unique identifier.",
				Required:            true,
				Computed:            false,
				Optional:            false,
			},
			"spec": schema.SingleNestedAttribute{
				MarkdownDescription: "The specification for the stream.",
				Required:            true,
				Computed:            false,
				Optional:            false,
				Attributes: map[string]schema.Attribute{
					"display_name": schema.StringAttribute{
						MarkdownDescription: "The display name of the stream.",
						Required:            true,
					},
					"logs": schema.SingleNestedAttribute{
						MarkdownDescription: "Logs stream configuration.",
						Optional:            true,
						Attributes:          map[string]schema.Attribute{},
					},
					"metrics": schema.SingleNestedAttribute{
						MarkdownDescription: "Metrics stream configuration.",
						Optional:            true,
						Attributes:          map[string]schema.Attribute{},
					},
				},
			},
		},
	}
}

func (s *StreamResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data StreamResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq, err := data.toCreateRequest()
	if err != nil {
		resp.Diagnostics.AddError("failed to create request", err.Error())
		return
	}

	createResp, err := s.client.CreateStream(ctx, connect.NewRequest(createReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(createResp.Msg.Stream)
	if diag := resp.State.Set(ctx, &data); diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}

	// wait for the stream to become ready
	conf := retry.StateChangeConf{
		Pending: []string{
			telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_UNSPECIFIED.String(),
			telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_INACTIVE.String(),
		},
		Target: []string{telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE.String()},
		Refresh: func() (result any, state string, err error) {
			getReq := data.toGetRequest()
			resp, err := s.client.GetStream(ctx, connect.NewRequest(getReq))
			if err != nil {
				tflog.Error(ctx, "failed to fetch telecaster stream resource", map[string]interface{}{
					"error": err,
				})
				return nil, telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_UNSPECIFIED.String(), err
			}

			return resp.Msg.Stream, resp.Msg.Stream.Status.String(), nil
		},
		Timeout: 20 * time.Minute,
	}

	rawStream, err := conf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	stream, ok := rawStream.(*telecastertypesv1beta1.TelemetryStream)
	if !ok {
		resp.Diagnostics.AddError("Unexpected type received when waiting for resource creation", fmt.Sprintf("expected *telecastertypesv1beta1.TelemetryStream, got %T", rawStream))
		return
	}

	data.Set(stream)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (s *StreamResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data StreamResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := s.client.DeleteStream(ctx, connect.NewRequest(data.toDeleteRequest())); err != nil {
		if coreweave.IsNotFoundError(err) {
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	conf := retry.StateChangeConf{
		Pending: []string{
			telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE.String(),
			telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_INACTIVE.String(),
		},
		Target: []string{""},
		Refresh: func() (result interface{}, state string, err error) {
			getReq := data.toGetRequest()
			resp, err := s.client.GetStream(ctx, connect.NewRequest(getReq))
			if err != nil {
				var connectErr *connect.Error
				if errors.As(err, &connectErr) && connectErr.Code() == connect.CodeNotFound {
					return struct{}{}, "", nil
				}

				tflog.Error(ctx, "failed to fetch telecaster stream", map[string]interface{}{
					"error": err.Error(),
				})
				return nil, telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_UNSPECIFIED.String(), err
			}

			return resp.Msg.Stream, resp.Msg.Stream.Status.String(), nil
		},
		Timeout: 20 * time.Minute,
	}

	if _, err := conf.WaitForStateContext(ctx); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
}

func (s *StreamResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data StreamResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := s.client.GetStream(ctx, connect.NewRequest(data.toGetRequest()))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(getResp.Msg.Stream)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (s *StreamResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data StreamResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateReq, err := data.toUpdateRequest()
	if err != nil {
		resp.Diagnostics.AddError("failed to create request", err.Error())
		return
	}

	if _, err := s.client.UpdateStream(ctx, connect.NewRequest(updateReq)); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	conf := retry.StateChangeConf{
		Pending: []string{
			telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_UNSPECIFIED.String(),
			telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_INACTIVE.String(),
		},
		Target: []string{telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE.String()},
		Refresh: func() (result any, state string, err error) {
			getReq := data.toGetRequest()
			resp, err := s.client.GetStream(ctx, connect.NewRequest(getReq))
			if err != nil {
				tflog.Error(ctx, "failed to fetch telecaster stream resource", map[string]interface{}{
					"error": err,
				})
				return nil, telecastertypesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_UNSPECIFIED.String(), err
			}

			return resp.Msg.Stream, resp.Msg.Stream.Status.String(), nil
		},
		Timeout: 20 * time.Minute,
	}

	rawStream, err := conf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	stream, ok := rawStream.(*telecastertypesv1beta1.TelemetryStream)
	if !ok {
		resp.Diagnostics.AddError("Unexpected type received when waiting for resource update", fmt.Sprintf("expected *telecastertypesv1beta1.TelemetryStream, got %T", rawStream))
		return
	}

	data.Set(stream)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

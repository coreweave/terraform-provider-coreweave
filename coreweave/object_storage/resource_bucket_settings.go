package objectstorage

import (
	"context"
	"fmt"

	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	_ resource.ResourceWithConfigure   = &BucketSettingsResource{}
	_ resource.ResourceWithImportState = &BucketSettingsResource{}
)

func NewBucketSettingsResource() resource.Resource {
	return &BucketSettingsResource{}
}

// BucketSettingsResource is the resource implementation.
type BucketSettingsResource struct {
	client *coreweave.Client
}

type BucketSettingsModel struct {
	Bucket              types.String `tfsdk:"bucket"`
	AuditLoggingEnabled types.Bool   `tfsdk:"audit_logging_enabled"`
}

func (s *BucketSettingsModel) Set(settings *cwobjectv1.CWObjectBucketSettings) {
	if settings == nil {
		return
	}

	if settings.AuditLoggingEnabled != nil {
		s.AuditLoggingEnabled = types.BoolValue(settings.AuditLoggingEnabled.Value)
	} else {
		s.AuditLoggingEnabled = types.BoolNull()
	}
}

func (s *BucketSettingsModel) ToProtoObject() *cwobjectv1.CWObjectBucketSettings {
	settings := cwobjectv1.CWObjectBucketSettings{}
	if !s.AuditLoggingEnabled.IsNull() {
		settings.SetAuditLoggingEnabled(wrapperspb.Bool(s.AuditLoggingEnabled.ValueBool()))
	}
	return &settings
}

func (b *BucketSettingsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	b.client = client
}

func (b *BucketSettingsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage_bucket_settings"
}

func (b *BucketSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data BucketSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	setReq := cwobjectv1.SetBucketSettingsRequest{
		BucketName: data.Bucket.ValueString(),
		Settings:   data.ToProtoObject(),
	}

	_, err := b.client.CWObjectClient.SetBucketSettings(ctx, connect.NewRequest(&setReq))
	if err != nil {
		resp.Diagnostics.AddError("Error creating bucket settings", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (b *BucketSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BucketSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := cwobjectv1.SetBucketSettingsRequest{
		BucketName: data.Bucket.ValueString(),
		Settings:   &cwobjectv1.CWObjectBucketSettings{},
	}

	_, err := b.client.CWObjectClient.SetBucketSettings(ctx, connect.NewRequest(&deleteReq))
	if err != nil {
		resp.Diagnostics.AddError("Error deleting bucket settings", err.Error())
		return
	}

	resp.State.RemoveResource(ctx)
}

func (b *BucketSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data BucketSettingsModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := b.client.CWObjectClient.GetBucketInfo(ctx, connect.NewRequest(&cwobjectv1.GetBucketInfoRequest{
		BucketName: data.Bucket.ValueString(),
	}))
	if err != nil {
		resp.Diagnostics.AddError("Error reading bucket settings", err.Error())
		return
	}

	data.Set(getResp.Msg.Info.Settings)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (b *BucketSettingsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages settings for an Object Storage Bucket.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Description: "The name of the bucket to manage settings for.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"audit_logging_enabled": schema.BoolAttribute{
				Description: "Whether audit logging is enabled for the bucket.",
				Required:    true,
			},
		},
	}
}

func (b *BucketSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data BucketSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	setReq := cwobjectv1.SetBucketSettingsRequest{
		BucketName: data.Bucket.ValueString(),
		Settings:   data.ToProtoObject(),
	}

	_, err := b.client.CWObjectClient.SetBucketSettings(ctx, connect.NewRequest(&setReq))
	if err != nil {
		resp.Diagnostics.AddError("Error creating bucket settings", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (b *BucketSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	getReq := cwobjectv1.GetBucketInfoRequest{
		BucketName: req.ID,
	}
	getResp, err := b.client.CWObjectClient.GetBucketInfo(ctx, connect.NewRequest(&getReq))
	if err != nil {
		resp.Diagnostics.AddError("Error reading bucket settings", err.Error())
		return
	}

	var data BucketSettingsModel
	data.Bucket = types.StringValue(req.ID)
	data.Set(getResp.Msg.Info.Settings)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

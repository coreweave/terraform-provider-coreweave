package objectstorage

import (
	"bytes"
	"context"
	"fmt"

	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/zclconf/go-cty/cty"
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

// BucketSettingsResourceModel is an alias for BucketSettingsModel for consistency with other resources
type BucketSettingsResourceModel = BucketSettingsModel

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

	_, err := b.client.SetBucketSettings(ctx, connect.NewRequest(&setReq))
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
		Settings: &cwobjectv1.CWObjectBucketSettings{
			AuditLoggingEnabled: wrapperspb.Bool(false),
		},
	}

	_, err := b.client.SetBucketSettings(ctx, connect.NewRequest(&deleteReq))
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

	getResp, err := b.client.GetBucketInfo(ctx, connect.NewRequest(&cwobjectv1.GetBucketInfoRequest{
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
				Description: "Whether audit logging is enabled for the bucket. Note: please contact support to enable audit logging for your organization before enabling.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
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

	_, err := b.client.SetBucketSettings(ctx, connect.NewRequest(&setReq))
	if err != nil {
		resp.Diagnostics.AddError("Error updating bucket settings", err.Error())
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
	getResp, err := b.client.GetBucketInfo(ctx, connect.NewRequest(&getReq))
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

// MustRenderBucketSettingsResource renders a bucket settings resource to HCL for testing purposes
func MustRenderBucketSettingsResource(_ context.Context, name string, settings *BucketSettingsResourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{"coreweave_object_storage_bucket_settings", name})
	resourceBody := resource.Body()

	// bucket attribute
	resourceBody.SetAttributeRaw("bucket", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(settings.Bucket.ValueString())}})

	// audit_logging_enabled attribute
	if !settings.AuditLoggingEnabled.IsNull() {
		resourceBody.SetAttributeValue("audit_logging_enabled", cty.BoolVal(settings.AuditLoggingEnabled.ValueBool()))
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

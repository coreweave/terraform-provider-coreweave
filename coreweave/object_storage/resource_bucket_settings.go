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
	"github.com/hashicorp/terraform-plugin-framework-validators/int32validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/zclconf/go-cty/cty"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	_ resource.ResourceWithConfigure      = &BucketSettingsResource{}
	_ resource.ResourceWithImportState    = &BucketSettingsResource{}
	_ resource.ResourceWithValidateConfig = &BucketSettingsResource{}
)

func NewBucketSettingsResource() resource.Resource {
	return &BucketSettingsResource{}
}

// BucketSettingsResource is the resource implementation.
type BucketSettingsResource struct {
	client *coreweave.Client
}

type BucketSettingsModel struct {
	Bucket                     types.String `tfsdk:"bucket"`
	AuditLoggingEnabled        types.Bool   `tfsdk:"audit_logging_enabled"`
	ArchiveEnabled             types.Bool   `tfsdk:"archive_enabled"`
	ArchiveAfterLastAccessDays types.Int32  `tfsdk:"archive_after_last_access_days"`
	CapacityCapBytes           types.Int64  `tfsdk:"capacity_cap_bytes"`
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

	if settings.ArchiveEnabled != nil {
		s.ArchiveEnabled = types.BoolValue(settings.ArchiveEnabled.Value)
	} else {
		s.ArchiveEnabled = types.BoolNull()
	}

	if settings.ArchiveAfterLastAccessDays != nil {
		s.ArchiveAfterLastAccessDays = types.Int32Value(settings.ArchiveAfterLastAccessDays.Value)
	} else {
		s.ArchiveAfterLastAccessDays = types.Int32Null()
	}

	// Read the cap from the read-only configured field (the write oneof isn't returned).
	if settings.ConfiguredCapacityCapBytes != nil {
		s.CapacityCapBytes = types.Int64Value(int64(settings.ConfiguredCapacityCapBytes.Value)) //nolint:gosec
	} else {
		s.CapacityCapBytes = types.Int64Null()
	}
}

func (s *BucketSettingsModel) ToProtoObject() *cwobjectv1.CWObjectBucketSettings {
	// Unknown values must be skipped as well as null ones: ValueBool/ValueInt32
	// return the zero value for an unknown, which would send a meaningless 0 the
	// API validates and rejects rather than omitting the field.
	settings := cwobjectv1.CWObjectBucketSettings{}
	if !s.AuditLoggingEnabled.IsNull() && !s.AuditLoggingEnabled.IsUnknown() {
		settings.SetAuditLoggingEnabled(wrapperspb.Bool(s.AuditLoggingEnabled.ValueBool()))
	}
	if !s.ArchiveEnabled.IsNull() && !s.ArchiveEnabled.IsUnknown() {
		settings.SetArchiveEnabled(wrapperspb.Bool(s.ArchiveEnabled.ValueBool()))
	}
	if !s.ArchiveAfterLastAccessDays.IsNull() && !s.ArchiveAfterLastAccessDays.IsUnknown() {
		settings.SetArchiveAfterLastAccessDays(wrapperspb.Int32(s.ArchiveAfterLastAccessDays.ValueInt32()))
	}
	// A known value (0 included) sets the cap; null/unknown is skipped. Update
	// also strips a state-copied value via omitUnconfiguredCapacityCap.
	if !s.CapacityCapBytes.IsNull() && !s.CapacityCapBytes.IsUnknown() {
		settings.SetCapacityCapBytes(uint64(s.CapacityCapBytes.ValueInt64())) //nolint:gosec
	}
	return &settings
}

// omitUnconfiguredCapacityCap keeps an Optional+Computed value copied from
// refreshed state from becoming a write; only a configured cap may set it.
func omitUnconfiguredCapacityCap(settings *cwobjectv1.CWObjectBucketSettings, configured types.Int64) {
	if configured.IsNull() || configured.IsUnknown() {
		settings.ClearCapacityCapUpdate()
	}
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

	setResp, err := b.client.SetBucketSettings(ctx, connect.NewRequest(&setReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(setResp.Msg.Settings)

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

	settings := cwobjectv1.CWObjectBucketSettings{
		AuditLoggingEnabled: wrapperspb.Bool(false),
	}

	// Only disable archive if this resource had it enabled. Sending the field at
	// all requires the bucket archive entitlement, so an unconditional disable
	// would make the resource impossible to destroy for unentitled orgs.
	if data.ArchiveEnabled.ValueBool() {
		settings.ArchiveEnabled = wrapperspb.Bool(false)
	}

	// Destroy resets bucket settings. Clear a cap reported in state, but omit the
	// instruction when no cap exists so destroy needs no capacity-cap permission.
	if !data.CapacityCapBytes.IsNull() {
		settings.SetClearCapacityCap(&emptypb.Empty{})
	}

	deleteReq := cwobjectv1.SetBucketSettingsRequest{
		BucketName: data.Bucket.ValueString(),
		Settings:   &settings,
	}

	_, err := b.client.SetBucketSettings(ctx, connect.NewRequest(&deleteReq))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
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
		// The bucket is gone out-of-band; drop the settings so a subsequent plan
		// can recreate them rather than reconciling against a bucket that is not there.
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
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
		Description: "Manage settings for a CoreWeave AI Object Storage bucket.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Description: "The name of the bucket to manage settings for.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"audit_logging_enabled": schema.BoolAttribute{
				Description: "Whether audit logging is enabled for the bucket. Contact support to enable audit logging for your organization before enabling this setting.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			// Deliberately has no default, unlike audit_logging_enabled above.
			// A default is a known value, but unentitled orgs must leave the value
			// unknown, so the provider handles it.
			"archive_enabled": schema.BoolAttribute{
				MarkdownDescription: "When true, idle STANDARD objects are archived to STANDARD_IA after `archive_after_last_access_days` without access. Your organization must be entitled to configure this setting.",
				Optional:            true,
				Computed:            true,
			},
			// Deliberately not Computed: given the ValidateConfig pairing with
			// archive_enabled, an omitted value always means archive is not being
			// turned on, so it should plan as a definite null rather than an
			// unknown for the server to fill.
			"archive_after_last_access_days": schema.Int32Attribute{
				MarkdownDescription: "Days since last access (or creation if never accessed) before a STANDARD object version is archived to STANDARD_IA. The default minimum is 60; your organization's entitlement may permit a different minimum, so the effective floor is validated server-side. Required when `archive_enabled` is true, and rejected otherwise.",
				Optional:            true,
				Validators: []validator.Int32{
					int32validator.AtLeast(1),
				},
			},
			// Optional+Computed like archive_enabled: omit leaves the cap unchanged.
			"capacity_cap_bytes": schema.Int64Attribute{
				MarkdownDescription: "Maximum number of STANDARD-class bytes the bucket may store. New STANDARD writes are rejected once bucket usage would exceed this cap; `0` is a valid cap that blocks all new STANDARD writes. Omit to leave the cap unchanged. Deleting this bucket settings resource clears any cap reported by the service, including one configured outside Terraform. Your organization must be entitled to configure this setting.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.Int64{
					int64validator.AtLeast(0),
				},
			},
		},
	}
}

func (b *BucketSettingsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data BucketSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// archive_after_last_access_days is required when archive is enabled. Skip
	// validation while either value is unknown (e.g. derived from another
	// resource) since it cannot be evaluated until apply.
	if data.ArchiveEnabled.IsUnknown() || data.ArchiveAfterLastAccessDays.IsUnknown() {
		return
	}

	if data.ArchiveEnabled.ValueBool() && data.ArchiveAfterLastAccessDays.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("archive_after_last_access_days"),
			"Missing archive_after_last_access_days",
			"archive_after_last_access_days must be set when archive_enabled is true.",
		)
	}

	if !data.ArchiveEnabled.ValueBool() && !data.ArchiveAfterLastAccessDays.IsNull() {
		resp.Diagnostics.AddAttributeError(
			path.Root("archive_after_last_access_days"),
			"Unexpected archive_after_last_access_days",
			"archive_after_last_access_days can only be set when archive_enabled is true.",
		)
	}
}

func (b *BucketSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data BucketSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	var config BucketSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Optional+Computed copies a state cap into the plan; strip it unless the
	// practitioner configured one, so an unrelated update never re-sends it.
	settings := data.ToProtoObject()
	omitUnconfiguredCapacityCap(settings, config.CapacityCapBytes)

	setReq := cwobjectv1.SetBucketSettingsRequest{
		BucketName: data.Bucket.ValueString(),
		Settings:   settings,
	}

	setResp, err := b.client.SetBucketSettings(ctx, connect.NewRequest(&setReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(setResp.Msg.Settings)

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
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
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

	// archive_enabled attribute
	if !settings.ArchiveEnabled.IsNull() {
		resourceBody.SetAttributeValue("archive_enabled", cty.BoolVal(settings.ArchiveEnabled.ValueBool()))
	}

	// archive_after_last_access_days attribute
	if !settings.ArchiveAfterLastAccessDays.IsNull() {
		resourceBody.SetAttributeValue("archive_after_last_access_days", cty.NumberIntVal(int64(settings.ArchiveAfterLastAccessDays.ValueInt32())))
	}

	// capacity_cap_bytes attribute
	if !settings.CapacityCapBytes.IsNull() {
		resourceBody.SetAttributeValue("capacity_cap_bytes", cty.NumberIntVal(settings.CapacityCapBytes.ValueInt64()))
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

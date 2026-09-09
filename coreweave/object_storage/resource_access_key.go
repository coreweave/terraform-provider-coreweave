package objectstorage

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	_ resource.ResourceWithImportState = &AccessKeyResource{}
	_ resource.ResourceWithModifyPlan  = &AccessKeyResource{}
)

func NewAccessKeyResource() resource.Resource { return &AccessKeyResource{} }

type AccessKeyResource struct{ client *coreweave.Client }

type accessKeyModel struct {
	ID              types.String `tfsdk:"id"`
	SecretKey       types.String `tfsdk:"secret_key"`
	DurationSeconds types.Int64  `tfsdk:"duration_seconds"`
	Attributes      types.Map    `tfsdk:"attributes"`
	PrincipalName   types.String `tfsdk:"principal_name"`
	OrgID           types.String `tfsdk:"org_id"`
	Expiry          types.String `tfsdk:"expiry"`
	Status          types.String `tfsdk:"status"`
}

func (r *AccessKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage_access_key"
}

func (r *AccessKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: description, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
	}
	secret := computed("Secret returned only at creation. Preserved during refresh; unavailable after import. Sensitive values remain in Terraform state and state backups.")
	secret.Sensitive = true
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one AI Object Storage access key for the authenticated caller. Existing organization policies determine its permissions. Destroy revokes only this key.\n\n" +
			"Set `duration_seconds` explicitly when creating a key: zero creates a permanent key. Changes to duration or attributes replace the key. Use `terraform apply -replace=coreweave_object_storage_access_key.example` for manual rotation. There is no background renewal; distribute the new secret to consumers before removing an old key, using separate resources when an overlap is needed.\n\n" +
			"Refresh removes missing keys from state, so the next apply recreates them when a duration is configured. Expired and inactive keys remain managed, with their API status and a warning; they are not automatically rotated or reactivated. If the service later removes an expired key, it is treated as missing.\n\n" +
			"Import uses the key ID. The secret and original duration cannot be recovered and remain null. Omit `duration_seconds` in the imported resource configuration to keep the existing key; setting it explicitly plans replacement. Omit attributes to adopt the imported values, or configure the same values. Supply an explicit duration before intentionally replacing an imported key.\n\n" +
			"Protect access to Terraform state and backups: marking the secret sensitive hides normal CLI output but does not encrypt or omit it from state. Key creation is sent once without automatic retries. If its response is lost, a key may exist whose ID and secret were not saved; inspect keys using the service tooling before trying again.",
		Attributes: map[string]schema.Attribute{
			"id":               computed("Access-key ID. Used for import and individual revocation."),
			"secret_key":       secret,
			"duration_seconds": schema.Int64Attribute{Optional: true, MarkdownDescription: "Required for creation; integer from 0 to 4294967295. Zero means permanent. Omit only when retaining an imported key whose original duration is unavailable. Changing this value replaces the key.", Validators: []validator.Int64{int64validator.Between(0, math.MaxUint32)}, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"attributes":       schema.MapAttribute{Optional: true, Computed: true, ElementType: types.StringType, MarkdownDescription: "Creation attributes, at most 15 entries. Keys must be lowercase DNS labels of 1–63 characters and must not start with cw-, role, or groups. Values must be Kubernetes label values of at most 63 characters (empty allowed). Changes replace the key. Omitted values are adopted during import.", Validators: []validator.Map{accessKeyAttributesValidator{}}, PlanModifiers: []planmodifier.Map{mapplanmodifier.RequiresReplace(), mapplanmodifier.UseStateForUnknown()}},
			"principal_name":   computed("Principal determined by the authenticated caller."),
			"org_id":           computed("Organization cloud ID reported by the API."),
			"expiry":           computed("Expiry in RFC 3339 UTC format, or null for a permanent key (including an absent or year-one API expiry)."),
			"status":           computed("Read-only API status, such as ACTIVE, EXPIRED, or FULL_SUSPENDED. The provider never changes principal-wide status."),
		},
	}
}

func (r *AccessKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *coreweave.Client, got %T.", req.ProviderData))
	}
}

func (r *AccessKeyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var data accessKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.State.Raw.IsNull() && data.DurationSeconds.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("duration_seconds"), "Duration required for creation", "Set duration_seconds explicitly before creating or replacing this key; use zero for a permanent key. Only imported keys may retain an unknown original duration by omitting this setting.")
	}
}

func (r *AccessKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data accessKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.DurationSeconds.IsNull() || data.DurationSeconds.IsUnknown() {
		resp.Diagnostics.AddError("Duration required for creation", "Set duration_seconds explicitly before creating or replacing this key.")
		return
	}
	attributes := map[string]string{}
	if !data.Attributes.IsUnknown() {
		resp.Diagnostics.Append(data.Attributes.ElementsAs(ctx, &attributes, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	// Minting is not idempotent: a lost response cannot be recovered by repeating it.
	created, err := r.client.CreateAccessKeyFromJWT(coreweave.WithoutRetries(ctx), connect.NewRequest(&cwobjectv1.CreateAccessKeyFromJWTRequest{DurationSeconds: wrapperspb.UInt32(uint32(data.DurationSeconds.ValueInt64())), Attributes: attributes})) // #nosec G115 -- schema validates the uint32 range.
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		resp.Diagnostics.AddWarning("Creation outcome may be unknown", "The create request was not retried. If the response was lost, inspect access keys with the service tooling before trying again; a created secret cannot be recovered.")
		return
	}
	if created.Msg.AccessKeyId == "" {
		resp.Diagnostics.AddError("Invalid access-key response", "The API returned no key ID. Inspect access keys with the service tooling before trying again.")
		return
	}
	data.ID = types.StringValue(created.Msg.AccessKeyId)
	data.SecretKey = types.StringValue(created.Msg.SecretKey)
	data.PrincipalName = types.StringValue(created.Msg.PrincipalName)
	data.Expiry = accessKeyExpiry(created.Msg.Expiry)
	data.OrgID = types.StringNull()
	data.Status = types.StringNull()
	if data.Attributes.IsUnknown() || data.Attributes.IsNull() {
		attributes := created.Msg.Attributes
		if attributes == nil {
			attributes = map[string]string{}
		}
		var d diag.Diagnostics
		data.Attributes, d = types.MapValueFrom(ctx, types.StringType, attributes)
		resp.Diagnostics.Append(d...)
	}
	// Persist the creation-only secret and ID before any fallible follow-up read.
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if created.Msg.SecretKey == "" {
		resp.Diagnostics.AddError("Missing access-key secret", "The API returned a key ID but no secret. The key remains tracked so it can be revoked; replace it to obtain a usable secret.")
		return
	}
	r.readInfo(ctx, &data, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func accessKeyExpiry(expiry *timestamppb.Timestamp) types.String {
	if expiry == nil || expiry.AsTime().IsZero() {
		return types.StringNull()
	}
	return types.StringValue(expiry.AsTime().UTC().Format(time.RFC3339Nano))
}

func (r *AccessKeyResource) readInfo(ctx context.Context, data *accessKeyModel, diagnostics *diag.Diagnostics) {
	info, err := r.client.GetAccessKeyInfo(ctx, connect.NewRequest(&cwobjectv1.GetAccessKeyInfoRequest{AccessKeyId: data.ID.ValueString()}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, diagnostics)
		return
	}
	r.setInfo(ctx, data, info.Msg.Info, diagnostics)
}

func (r *AccessKeyResource) setInfo(ctx context.Context, data *accessKeyModel, info *cwobjectv1.AccessKeyInfo, diagnostics *diag.Diagnostics) {
	if info == nil || info.AccessKeyId != data.ID.ValueString() {
		diagnostics.AddError("Invalid access-key response", "The API returned missing or mismatched key information. Existing state has been preserved.")
		return
	}
	data.PrincipalName = types.StringValue(info.PrincipalName)
	data.OrgID = types.StringValue(info.OrgId)
	data.Expiry = accessKeyExpiry(info.Expiry)
	data.Status = types.StringValue(info.Status)
	// Neither secret nor original duration is available from GetAccessKeyInfo.
	attributes := info.Attributes
	if attributes == nil {
		attributes = map[string]string{}
	}
	var d diag.Diagnostics
	data.Attributes, d = types.MapValueFrom(ctx, types.StringType, attributes)
	diagnostics.Append(d...)

	if info.Status != "ACTIVE" {
		diagnostics.AddWarning("Access key is not active", "The key remains managed and will not be automatically rotated or reactivated. API status: "+info.Status+". Use explicit replacement to rotate it.")
	}
}

func (r *AccessKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data accessKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	info, err := r.client.GetAccessKeyInfo(ctx, connect.NewRequest(&cwobjectv1.GetAccessKeyInfoRequest{AccessKeyId: data.ID.ValueString()}))
	if coreweave.IsNotFoundError(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	r.setInfo(ctx, &data, info.Msg.Info, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AccessKeyResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Access-key replacement required", "Creation settings cannot be updated in place; replace this resource.")
}

func (r *AccessKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data accessKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.RevokeAccessKeyByAccessKey(ctx, connect.NewRequest(&cwobjectv1.RevokeAccessKeyByAccessKeyRequest{AccessKey: data.ID.ValueString()}))
	if err != nil && !coreweave.IsNotFoundError(err) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
	}
}

func (r *AccessKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		resp.Diagnostics.AddError("Access-key ID required", "Import requires a nonempty access-key ID.")
		return
	}
	data := accessKeyModel{ID: types.StringValue(id), Attributes: types.MapNull(types.StringType)}
	r.readInfo(ctx, &data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	resp.Diagnostics.AddWarning("Imported credential limitations", "The secret and original duration cannot be recovered. Omit duration_seconds to retain this key; setting it plans replacement. Supply an explicit duration when intentionally rotating an imported key.")
}

var accessKeyAttributeName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var accessKeyAttributeValue = regexp.MustCompile(`^([A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?)?$`)

type accessKeyAttributesValidator struct{}

func (accessKeyAttributesValidator) Description(context.Context) string {
	return "At most 15 attributes using non-reserved DNS label keys and Kubernetes label values."
}
func (v accessKeyAttributesValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (accessKeyAttributesValidator) ValidateMap(_ context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if len(req.ConfigValue.Elements()) > 15 {
		resp.Diagnostics.AddAttributeError(req.Path, "Too many attributes", "At most 15 attributes are allowed.")
	}
	for key, value := range req.ConfigValue.Elements() {
		if len(key) > 63 || !accessKeyAttributeName.MatchString(key) || strings.HasPrefix(key, "cw-") || strings.HasPrefix(key, "role") || strings.HasPrefix(key, "groups") {
			resp.Diagnostics.AddAttributeError(req.Path.AtMapKey(key), "Invalid attribute key", "Keys must be DNS labels of 1–63 characters and cannot start with cw-, role, or groups.")
		}
		if value.IsUnknown() {
			continue
		}
		text, ok := value.(types.String)
		if !ok || text.IsNull() || len(text.ValueString()) > 63 || !accessKeyAttributeValue.MatchString(text.ValueString()) {
			resp.Diagnostics.AddAttributeError(req.Path.AtMapKey(key), "Invalid attribute value", "Values must be Kubernetes label values of at most 63 characters; empty strings are allowed, null is not.")
		}
	}
}

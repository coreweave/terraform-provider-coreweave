package objectstorage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/zclconf/go-cty/cty"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &BucketInventoryResource{}
	_ resource.ResourceWithImportState = &BucketInventoryResource{}
)

const (
	// ErrNoSuchInventoryConfiguration is the S3 error code returned when an
	// inventory configuration does not exist for the given bucket + id.
	ErrNoSuchInventoryConfiguration string = "NoSuchConfiguration"
)

// NewBucketInventoryResource returns a new resource instance.
func NewBucketInventoryResource() resource.Resource {
	return &BucketInventoryResource{}
}

// BucketInventoryResource is the resource implementation.
type BucketInventoryResource struct {
	client *coreweave.Client
}

// BucketInventoryResourceModel maps resource schema data.
type BucketInventoryResourceModel struct {
	Bucket  				types.String           	`tfsdk:"bucket"` // source bucket
	Name					types.String           	`tfsdk:"name"`   // inventory configuration name
	Enabled 				types.Bool             	`tfsdk:"enabled"`
	IncludedObjectVersions 	types.String 			`tfsdk:"included_object_versions"`
	OptionalFields 			types.Set              	`tfsdk:"optional_fields"`
	Filter 					*InventoryFilterModel 	`tfsdk:"filter"` // nested block
	Schedule 				*ScheduleModel 			`tfsdk:"schedule"` // nested block
	Destination 			*DestinationModel 		`tfsdk:"destination"` // nested block
}

type InventoryFilterModel struct {
	Prefix types.String `tfsdk:"prefix"`
}

type ScheduleModel struct {
	Frequency types.String `tfsdk:"frequency"`
}

type DestinationModel struct {
	Bucket *BucketModel `tfsdk:"bucket"` // nested block
}

type BucketModel struct {
	BucketArn types.String `tfsdk:"bucket_arn"`
	Format types.String `tfsdk:"format"`
	Prefix types.String `tfsdk:"prefix"`
	AccountId types.String `tfsdk:"account_id"`
}

func (r *BucketInventoryResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage_bucket_inventory"
}

func (r *BucketInventoryResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Coreweave AI Object Storage bucket inventory configuration. [Learn more about inventory reporting](https://docs.coreweave.com/products/storage/object-storage)",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of source bucket to which the inventory configuration applies",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the inventory configuration. Must be unique within the bucket.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether the inventory configuration is enabled. Defaults to `true`.",
			},
			"included_object_versions": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Specifies which object versions are included in the inventory results. Valid values are `All` and `Latest`.",
				Validators: []validator.String{stringvalidator.OneOf("All", "Latest")},
			},
			"optional_fields": schema.SetAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "List of optional fields to include in the inventory results",
				Validators: []validator.Set{setvalidator.ValueStringsAre(stringvalidator.OneOf("Size", "LastModifiedDate", "LastAccessedDate", "StorageClass", "ETag", "IsMultipartUploaded", "EncryptionStatus", "ChecksumAlgorithm"))},
			},
		},
		Blocks: map[string]schema.Block{
			"filter": schema.SingleNestedBlock{
				MarkdownDescription: "Limits the inventory report to objects matching a prefix.",
				Attributes: map[string]schema.Attribute{
					"prefix": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Source-object prefix to filter on.",
					},
				},
			},
			"schedule": schema.SingleNestedBlock{
				MarkdownDescription: "Schedule for generating the inventory report.",
				Attributes: map[string]schema.Attribute{
					"frequency": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "How frequently the report is generated: `Daily` or `Weekly`.",
						Validators:          []validator.String{stringvalidator.OneOf("Daily", "Weekly")},
					},
				},
				Validators: []validator.Object{objectvalidator.IsRequired()},
			},
			"destination": schema.SingleNestedBlock{
				MarkdownDescription: "Where the inventory report is written. May be the same bucket as the source.",
				Blocks: map[string]schema.Block{
					"bucket": schema.SingleNestedBlock{
						MarkdownDescription: "Destination bucket for the report (may equal the source bucket).",
						Attributes: map[string]schema.Attribute{
							"bucket_arn": schema.StringAttribute{
								Required:            true,
								MarkdownDescription: "ARN of the destination bucket.",
							},
							"format": schema.StringAttribute{
								Required:            true,
								MarkdownDescription: "Output format: `CSV`, `TSV`, `JSON`, `ORC`, or `Parquet`.",
								Validators:          []validator.String{stringvalidator.OneOf("CSV", "TSV", "JSON", "ORC", "Parquet")},
							},
							"prefix": schema.StringAttribute{
								Optional:            true,
								MarkdownDescription: "Prefix prepended to the report output path.",
							},
							"account_id": schema.StringAttribute{
								Optional:            true,
								MarkdownDescription: "Account ID of the destination bucket owner.",
							},
						},
						Validators: []validator.Object{objectvalidator.IsRequired()},
					},
				},
				Validators: []validator.Object{objectvalidator.IsRequired()},
			},
		},
	}
}

func (r *BucketInventoryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *coreweave.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.client = client
}

// expandInventoryConfiguration translates the Terraform model into the AWS SDK
// InventoryConfiguration that is sent to PutBucketInventoryConfiguration. It is
// the inverse of flattenInventoryConfiguration: anything set here must be read
// back there, or Terraform will report drift after apply.
func expandInventoryConfiguration(ctx context.Context, data *BucketInventoryResourceModel) (*s3types.InventoryConfiguration, diag.Diagnostics) {
	var diags diag.Diagnostics

	cfg := &s3types.InventoryConfiguration{
		Id:                     aws.String(data.Name.ValueString()),
		IsEnabled:              aws.Bool(data.Enabled.ValueBool()),
		IncludedObjectVersions: s3types.InventoryIncludedObjectVersions(data.IncludedObjectVersions.ValueString()),
	}

	// schedule (required) — frequency string is cast straight through.
	if data.Schedule != nil {
		cfg.Schedule = &s3types.InventorySchedule{
			Frequency: s3types.InventoryFrequency(data.Schedule.Frequency.ValueString()),
		}
	}

	// destination (required) — the nested bucket block carries the wire fields.
	if data.Destination != nil && data.Destination.Bucket != nil {
		b := data.Destination.Bucket
		dest := &s3types.InventoryS3BucketDestination{
			Bucket: aws.String(b.BucketArn.ValueString()),
			Format: s3types.InventoryFormat(b.Format.ValueString()),
		}
		if !b.Prefix.IsNull() {
			dest.Prefix = aws.String(b.Prefix.ValueString())
		}
		if !b.AccountId.IsNull() {
			dest.AccountId = aws.String(b.AccountId.ValueString())
		}
		cfg.Destination = &s3types.InventoryDestination{
			S3BucketDestination: dest,
		}
	}

	// filter (optional) — inventory filters only support a prefix.
	if data.Filter != nil && !data.Filter.Prefix.IsNull() {
		cfg.Filter = &s3types.InventoryFilter{
			Prefix: aws.String(data.Filter.Prefix.ValueString()),
		}
	}

	// optional_fields (optional) — Set of strings -> []InventoryOptionalField.
	if !data.OptionalFields.IsNull() && !data.OptionalFields.IsUnknown() {
		var fields []string
		diags.Append(data.OptionalFields.ElementsAs(ctx, &fields, false)...)
		if diags.HasError() {
			return nil, diags
		}
		cfg.OptionalFields = make([]s3types.InventoryOptionalField, 0, len(fields))
		for _, f := range fields {
			cfg.OptionalFields = append(cfg.OptionalFields, s3types.InventoryOptionalField(f))
		}
	}

	return cfg, diags
}


// eqInventoryConfiguration reports whether two inventory configurations are
// equivalent. OptionalFields are order-insensitive, so they are sorted before
// comparison; every other field is compared for exact (nil-safe) equality.
func eqInventoryConfiguration(a, b s3types.InventoryConfiguration) bool {
	if aws.ToString(a.Id) != aws.ToString(b.Id) {
		return false
	}
	if aws.ToBool(a.IsEnabled) != aws.ToBool(b.IsEnabled) {
		return false
	}
	if string(a.IncludedObjectVersions) != string(b.IncludedObjectVersions) {
		return false
	}

	// Schedule
	if (a.Schedule == nil) != (b.Schedule == nil) {
		return false
	}
	if a.Schedule != nil && string(a.Schedule.Frequency) != string(b.Schedule.Frequency) {
		return false
	}

	// Filter
	if (a.Filter == nil) != (b.Filter == nil) {
		return false
	}
	if a.Filter != nil && aws.ToString(a.Filter.Prefix) != aws.ToString(b.Filter.Prefix) {
		return false
	}

	// Destination
	var aDest, bDest *s3types.InventoryS3BucketDestination
	if a.Destination != nil {
		aDest = a.Destination.S3BucketDestination
	}
	if b.Destination != nil {
		bDest = b.Destination.S3BucketDestination
	}
	if (aDest == nil) != (bDest == nil) {
		return false
	}
	if aDest != nil {
		if aws.ToString(aDest.Bucket) != aws.ToString(bDest.Bucket) ||
			string(aDest.Format) != string(bDest.Format) ||
			aws.ToString(aDest.Prefix) != aws.ToString(bDest.Prefix) ||
			aws.ToString(aDest.AccountId) != aws.ToString(bDest.AccountId) {
			return false
		}
	}

	// OptionalFields (order-insensitive)
	toSorted := func(in []s3types.InventoryOptionalField) []string {
		out := make([]string, 0, len(in))
		for _, f := range in {
			out = append(out, string(f))
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(toSorted(a.OptionalFields), toSorted(b.OptionalFields))
}

func waitForInventoryConfig(parentCtx context.Context, client *s3.Client, bucket, id string, expected s3types.InventoryConfiguration) (*s3.GetBucketInventoryConfigurationOutput, error) {
	var out *s3.GetBucketInventoryConfigurationOutput
	err := coreweave.PollUntil("bucket inventory configuration", parentCtx, 5*time.Second, 5*time.Minute, func(ctx context.Context) (bool, error) {
		result, err := client.GetBucketInventoryConfiguration(ctx, &s3.GetBucketInventoryConfigurationInput{
			Bucket: aws.String(bucket),
			Id:     aws.String(id),
		})
		if err != nil {
			out = nil
			// A freshly-written config can 404 until it propagates; isTransientS3Error
			// treats 404 (and 5xx/429/408) as retryable so we keep polling.
			if isTransientS3Error(err) {
				return false, nil
			}
			return false, err
		}
		out = result

		if result.InventoryConfiguration == nil {
			return false, nil
		}
		return eqInventoryConfiguration(expected, *result.InventoryConfiguration), nil
	})
	return out, err
}

func (r *BucketInventoryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data BucketInventoryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := r.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	// model -> SDK
	invConfig, diags := expandInventoryConfiguration(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	invJSON, err := json.Marshal(invConfig)
	if err != nil {
		resp.Diagnostics.AddError("Failed to marshal inventory configuration to JSON", err.Error())
		return
	}
	tflog.Debug(ctx, "creating inventory configuration for bucket", map[string]any{
		"inventory": string(invJSON),
		"bucket":    data.Bucket.ValueString(),
		"id":        data.Name.ValueString(),
	})

	_, err = s3c.PutBucketInventoryConfiguration(ctx, &s3.PutBucketInventoryConfigurationInput{
		Bucket:                 aws.String(data.Bucket.ValueString()),
		Id:                     aws.String(data.Name.ValueString()),
		InventoryConfiguration: invConfig,
	})
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	// Persist state before waiting so a failure mid-wait doesn't orphan the
	// remote configuration we just created.
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Inventory configuration is eventually consistent, so poll the GET API
	// until it reflects what we just wrote before reading it back into state.
	result, err := waitForInventoryConfig(ctx, s3c, data.Bucket.ValueString(), data.Name.ValueString(), *invConfig)
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(flattenInventoryConfiguration(ctx, result.InventoryConfiguration, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// flattenInventoryConfiguration maps the AWS SDK InventoryConfiguration returned
// by GetBucketInventoryConfiguration back into the Terraform model. It is the
// inverse of expandInventoryConfiguration; the two must stay in lockstep or
// Terraform will report drift after apply. It mutates data in place and returns
// only diagnostics, since an inventory config is a single object rather than a
// list of rules. The bucket name is not part of the API payload, so data.Bucket
// is intentionally left untouched.
func flattenInventoryConfiguration(ctx context.Context, in *s3types.InventoryConfiguration, data *BucketInventoryResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if in == nil {
		return diags
	}

	data.Name = types.StringPointerValue(in.Id)
	data.Enabled = types.BoolPointerValue(in.IsEnabled)
	data.IncludedObjectVersions = types.StringValue(string(in.IncludedObjectVersions))

	// schedule
	if in.Schedule != nil {
		data.Schedule = &ScheduleModel{
			Frequency: types.StringValue(string(in.Schedule.Frequency)),
		}
	} else {
		data.Schedule = nil
	}

	// destination — the nested bucket block carries the wire fields.
	if in.Destination != nil && in.Destination.S3BucketDestination != nil {
		dest := in.Destination.S3BucketDestination
		data.Destination = &DestinationModel{
			Bucket: &BucketModel{
				BucketArn: types.StringPointerValue(dest.Bucket),
				Format:    types.StringValue(string(dest.Format)),
				Prefix:    types.StringPointerValue(dest.Prefix),
				AccountId: types.StringPointerValue(dest.AccountId),
			},
		}
	} else {
		data.Destination = nil
	}

	// filter — inventory filters only support a prefix.
	if in.Filter != nil {
		data.Filter = &InventoryFilterModel{
			Prefix: types.StringPointerValue(in.Filter.Prefix),
		}
	} else {
		data.Filter = nil
	}

	// optional_fields: []InventoryOptionalField -> Set of strings. Mirror expand
	// by leaving the set null when there are no fields, so a config that omits
	// optional_fields does not churn at plan time.
	if len(in.OptionalFields) == 0 {
		data.OptionalFields = types.SetNull(types.StringType)
	} else {
		fields := make([]string, 0, len(in.OptionalFields))
		for _, f := range in.OptionalFields {
			fields = append(fields, string(f))
		}
		setVal, d := types.SetValueFrom(ctx, types.StringType, fields)
		diags.Append(d...)
		data.OptionalFields = setVal
	}

	return diags
}

func (r *BucketInventoryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data BucketInventoryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := r.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	out, err := s3c.GetBucketInventoryConfiguration(ctx, &s3.GetBucketInventoryConfigurationInput{
		Bucket: aws.String(data.Bucket.ValueString()),
		Id:     aws.String(data.Name.ValueString()),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == ErrNoSuchInventoryConfiguration {
			// The inventory configuration no longer exists remotely; drop it from
			// state so Terraform plans to recreate it.
			resp.State.RemoveResource(ctx)
			return
		}
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(flattenInventoryConfiguration(ctx, out.InventoryConfiguration, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *BucketInventoryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data BucketInventoryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := r.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	// model -> SDK
	invConfig, diags := expandInventoryConfiguration(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// PutBucketInventoryConfiguration is an upsert, so update is the same write
	// path as create, keyed by bucket + id.
	_, err = s3c.PutBucketInventoryConfiguration(ctx, &s3.PutBucketInventoryConfigurationInput{
		Bucket:                 aws.String(data.Bucket.ValueString()),
		Id:                     aws.String(data.Name.ValueString()),
		InventoryConfiguration: invConfig,
	})
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	// Persist state before waiting so a failure mid-wait doesn't leave state
	// pointing at the pre-update configuration.
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Inventory configuration is eventually consistent, so poll the GET API
	// until it reflects the update before reading it back into state.
	result, err := waitForInventoryConfig(ctx, s3c, data.Bucket.ValueString(), data.Name.ValueString(), *invConfig)
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(flattenInventoryConfiguration(ctx, result.InventoryConfiguration, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *BucketInventoryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BucketInventoryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := r.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	_, err = s3c.DeleteBucketInventoryConfiguration(ctx, &s3.DeleteBucketInventoryConfigurationInput{
		Bucket: aws.String(data.Bucket.ValueString()),
		Id:     aws.String(data.Name.ValueString()),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && ((apiErr.ErrorCode() == ErrNoSuchInventoryConfiguration) || (apiErr.ErrorCode() == ErrNoSuchBucket)) {
			// The inventory configuration (or its bucket) is already gone; treat as
			// a successful delete so the resource is removed from state.
			return
		}

		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
	}
}

func (r *BucketInventoryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import ID format: "<bucket>:<name>". Inventory configurations are keyed by
	// both the bucket and the configuration id, so both are required to locate one.
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected import ID in the format \"<bucket>:<name>\", got: %q", req.ID),
		)
		return
	}

	data := BucketInventoryResourceModel{
		Bucket: types.StringValue(parts[0]),
		Name:   types.StringValue(parts[1]),
	}

	s3c, err := r.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}
	out, err := s3c.GetBucketInventoryConfiguration(ctx, &s3.GetBucketInventoryConfigurationInput{
		Bucket: aws.String(data.Bucket.ValueString()),
		Id:     aws.String(data.Name.ValueString()),
	})
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(flattenInventoryConfiguration(ctx, out.InventoryConfiguration, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// MustRenderBucketInventoryResource renders HCL for an inventory configuration.
// It mirrors the other object-storage Must* helpers used by acceptance tests.
// The bucket attribute is emitted as a raw token so callers can pass a resource
// reference (e.g. coreweave_object_storage_bucket.x.name); every other value is
// emitted as a literal.
func MustRenderBucketInventoryResource(ctx context.Context, name string, cfg *BucketInventoryResourceModel, dependsOn ...string) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	block := body.AppendNewBlock("resource", []string{"coreweave_object_storage_bucket_inventory", name})
	b := block.Body()

	// bucket rendered as a raw reference token, not a quoted string.
	b.SetAttributeRaw("bucket", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(cfg.Bucket.ValueString())}})

	// depends_on rendered as raw reference tokens (a list of resource addresses),
	// so the destination bucket's policy is applied before the inventory service
	// validates it can write reports to the destination.
	if len(dependsOn) > 0 {
		toks := hclwrite.Tokens{{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")}}
		for i, dep := range dependsOn {
			if i > 0 {
				toks = append(toks, &hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(",")})
			}
			toks = append(toks, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(dep)})
		}
		toks = append(toks, &hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")})
		b.SetAttributeRaw("depends_on", toks)
	}

	b.SetAttributeValue("name", cty.StringVal(cfg.Name.ValueString()))
	if !cfg.Enabled.IsNull() {
		b.SetAttributeValue("enabled", cty.BoolVal(cfg.Enabled.ValueBool()))
	}
	b.SetAttributeValue("included_object_versions", cty.StringVal(cfg.IncludedObjectVersions.ValueString()))

	if !cfg.OptionalFields.IsNull() {
		var fields []string
		cfg.OptionalFields.ElementsAs(ctx, &fields, false)
		vals := make([]cty.Value, 0, len(fields))
		for _, f := range fields {
			vals = append(vals, cty.StringVal(f))
		}
		if len(vals) > 0 {
			b.SetAttributeValue("optional_fields", cty.SetVal(vals))
		}
	}

	if cfg.Filter != nil {
		fb := b.AppendNewBlock("filter", nil).Body()
		if !cfg.Filter.Prefix.IsNull() {
			fb.SetAttributeValue("prefix", cty.StringVal(cfg.Filter.Prefix.ValueString()))
		}
	}

	if cfg.Schedule != nil {
		sb := b.AppendNewBlock("schedule", nil).Body()
		sb.SetAttributeValue("frequency", cty.StringVal(cfg.Schedule.Frequency.ValueString()))
	}

	if cfg.Destination != nil && cfg.Destination.Bucket != nil {
		db := b.AppendNewBlock("destination", nil).Body()
		bb := db.AppendNewBlock("bucket", nil).Body()
		dest := cfg.Destination.Bucket
		bb.SetAttributeValue("bucket_arn", cty.StringVal(dest.BucketArn.ValueString()))
		bb.SetAttributeValue("format", cty.StringVal(dest.Format.ValueString()))
		if !dest.Prefix.IsNull() {
			bb.SetAttributeValue("prefix", cty.StringVal(dest.Prefix.ValueString()))
		}
		if !dest.AccountId.IsNull() {
			bb.SetAttributeValue("account_id", cty.StringVal(dest.AccountId.ValueString()))
		}
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

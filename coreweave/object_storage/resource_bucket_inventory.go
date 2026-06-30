package objectstorage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	ErrNoSuchLifecycleConfiguration string = "NoSuchLifecycleConfiguration"
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
	Filter 					*FilterModel 			`tfsdk:"filter"` // nested block
	Schedule 				*ScheduleModel 			`tfsdk:"schedule"` // nested block
	Destination 			*DestinationModel 		`tfsdk:"destination"` // nested block
}

type FilterModel struct {
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

// ExpirationModel maps the expiration sub-block.
type ExpirationModel struct {
	Date                      types.String `tfsdk:"date"`
	Days                      types.Int32  `tfsdk:"days"`
	ExpiredObjectDeleteMarker types.Bool   `tfsdk:"expired_object_delete_marker"`
}

// TransitionModel maps the transition sub-block.
type TransitionModel struct {
	Date         types.String `tfsdk:"date"`
	Days         types.Int32  `tfsdk:"days"`
	StorageClass types.String `tfsdk:"storage_class"`
}

// NoncurrentVersionExpirationModel maps the noncurrent_version_expiration sub-block.
type NoncurrentVersionExpirationModel struct {
	NoncurrentDays          types.Int32 `tfsdk:"noncurrent_days"`
	NewerNoncurrentVersions types.Int32 `tfsdk:"newer_noncurrent_versions"`
}

// NoncurrentVersionTransitionModel maps the noncurrent_version_transition sub-block.
type NoncurrentVersionTransitionModel struct {
	NoncurrentDays          types.Int32  `tfsdk:"noncurrent_days"`
	NewerNoncurrentVersions types.Int32  `tfsdk:"newer_noncurrent_versions"`
	StorageClass            types.String `tfsdk:"storage_class"`
}

// AbortIncompleteMultipartModel maps the abort_incomplete_multipart_upload sub-block.
type AbortIncompleteMultipartModel struct {
	DaysAfterInitiation types.Int32 `tfsdk:"days_after_initiation"`
}

// TagModel maps a single tag in an AND filter.
type TagModel struct {
	Key   types.String `tfsdk:"key"`
	Value types.String `tfsdk:"value"`
}

// AndFilterModel maps the AND sub-block of filter.
type AndFilterModel struct {
	Prefix                types.String `tfsdk:"prefix"`
	Tags                  types.Map    `tfsdk:"tags"`
	ObjectSizeGreaterThan types.Int64  `tfsdk:"object_size_greater_than"`
	ObjectSizeLessThan    types.Int64  `tfsdk:"object_size_less_than"`
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
				Required:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether the inventory configuration is enabled",
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
				Validators: []validator.Set{setvalidator.OneOf("Size", "LastModifiedDate", "LastAccessedDate", "StorageClass", "ETag", "IsMultipartUploaded", "EncryptionStatus", "ChecksumAlgorithm")},
			}
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
				Attributes: map[string]schema.Attribute{
					"bucket": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Destination bucket for the report (may equal the source bucket).",
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

func parseISO8601(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
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

// cmpLifecycleRule returns -1 if a<b, +1 if a>b, or 0 if equal (nil-safe).
func cmpLifecycleRule(a, b s3types.LifecycleRule) int {
	// Key
	switch {
	case a.ID == nil && b.ID != nil:
		return -1
	case a.ID != nil && b.ID == nil:
		return 1
	case a.ID != nil && b.ID != nil:
		if *a.ID < *b.ID {
			return -1
		} else if *a.ID > *b.ID {
			return 1
		}
	}
	return 0
}

func hasNilPointer[T any](pointers ...*T) bool {
	for _, p := range pointers {
		if p == nil {
			return true
		}
	}

	return false
}

// eqLifecycleRule compares two LifecycleRule objects for exact equality. The
// deprecated top-level Prefix field is intentionally not compared — the
// provider never sets it on the wire, so both sides are always empty.
func eqLifecycleRule(a, b s3types.LifecycleRule) bool { //nolint:gocyclo
	// Compare simple scalar fields via aws.ToString / string conversion
	if aws.ToString(a.ID) != aws.ToString(b.ID) {
		return false
	}
	if string(a.Status) != string(b.Status) {
		return false
	}

	// Compare Expiration
	if (a.Expiration == nil) != (b.Expiration == nil) {
		return false
	}
	if a.Expiration != nil {
		if !hasNilPointer(a.Expiration.Date, b.Expiration.Date) && (!a.Expiration.Date.IsZero() || !b.Expiration.Date.IsZero()) {
			if !a.Expiration.Date.Equal(*b.Expiration.Date) {
				return false
			}
		}
		if aws.ToInt32(a.Expiration.Days) != aws.ToInt32(b.Expiration.Days) {
			return false
		}
		if aws.ToBool(a.Expiration.ExpiredObjectDeleteMarker) != aws.ToBool(b.Expiration.ExpiredObjectDeleteMarker) {
			return false
		}
	}

	// Compare NoncurrentVersionExpiration
	if (a.NoncurrentVersionExpiration == nil) != (b.NoncurrentVersionExpiration == nil) {
		return false
	}
	if a.NoncurrentVersionExpiration != nil {
		if aws.ToInt32(a.NoncurrentVersionExpiration.NoncurrentDays) != aws.ToInt32(b.NoncurrentVersionExpiration.NoncurrentDays) {
			return false
		}
		if aws.ToInt32(a.NoncurrentVersionExpiration.NewerNoncurrentVersions) != aws.ToInt32(b.NoncurrentVersionExpiration.NewerNoncurrentVersions) {
			return false
		}
	}

	// Compare AbortIncompleteMultipartUpload
	if (a.AbortIncompleteMultipartUpload == nil) != (b.AbortIncompleteMultipartUpload == nil) {
		return false
	}
	if a.AbortIncompleteMultipartUpload != nil {
		if aws.ToInt32(a.AbortIncompleteMultipartUpload.DaysAfterInitiation) != aws.ToInt32(b.AbortIncompleteMultipartUpload.DaysAfterInitiation) {
			return false
		}
	}

	// Compare Filter
	if (a.Filter == nil) != (b.Filter == nil) {
		return false
	}
	if a.Filter != nil {
		// Prefix
		if aws.ToString(a.Filter.Prefix) != aws.ToString(b.Filter.Prefix) {
			return false
		}
		// Tag
		if (a.Filter.Tag == nil) != (b.Filter.Tag == nil) {
			return false
		}
		if a.Filter.Tag != nil {
			if aws.ToString(a.Filter.Tag.Key) != aws.ToString(b.Filter.Tag.Key) ||
				aws.ToString(a.Filter.Tag.Value) != aws.ToString(b.Filter.Tag.Value) {
				return false
			}
		}
		// ObjectSize thresholds
		if aws.ToInt64(a.Filter.ObjectSizeGreaterThan) != aws.ToInt64(b.Filter.ObjectSizeGreaterThan) {
			return false
		}
		if aws.ToInt64(a.Filter.ObjectSizeLessThan) != aws.ToInt64(b.Filter.ObjectSizeLessThan) {
			return false
		}
		// And operator
		if (a.Filter.And == nil) != (b.Filter.And == nil) {
			return false
		}
		if a.Filter.And != nil {
			if aws.ToString(a.Filter.And.Prefix) != aws.ToString(b.Filter.And.Prefix) {
				return false
			}
			if aws.ToInt64(a.Filter.And.ObjectSizeGreaterThan) != aws.ToInt64(b.Filter.And.ObjectSizeGreaterThan) {
				return false
			}
			if aws.ToInt64(a.Filter.And.ObjectSizeLessThan) != aws.ToInt64(b.Filter.And.ObjectSizeLessThan) {
				return false
			}
			aTags := a.Filter.And.Tags
			bTags := b.Filter.And.Tags
			slices.SortFunc(aTags, cmpTag)
			slices.SortFunc(bTags, cmpTag)
			if !slices.EqualFunc(aTags, bTags, eqTag) {
				return false
			}
		}
	}

	return true
}

func waitForLifecycleConfig(parentCtx context.Context, client *s3.Client, bucket string, expected s3types.BucketLifecycleConfiguration) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	// make a sorted copy of expected rules
	exp := slices.SortedFunc(slices.Values(expected.Rules), cmpLifecycleRule)

	var out *s3.GetBucketLifecycleConfigurationOutput
	err := coreweave.PollUntil("bucket lifecycle configuration", parentCtx, 5*time.Second, 5*time.Minute, func(ctx context.Context) (bool, error) {
		result, err := client.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(bucket)})
		if err != nil {
			out = nil
			if isTransientS3Error(err) {
				return false, nil
			}
			return false, err
		}
		out = result

		// Make sorted a copy of the slice to sort for comparison, to avoid mutating the returned slice by reference.
		rules := slices.SortedFunc(slices.Values(result.Rules), cmpLifecycleRule)
		return slices.EqualFunc(exp, rules, eqLifecycleRule), nil
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

// flattenLifecycleRules turns AWS SDK LifecycleRule objects into our Terraform
// model. The preserve argument is the prior model (plan or state) before the
// API round-trip; it carries client-side-only fields that the API does not
// echo back — currently just the deprecated rule.prefix.
//
// Matching is by rule ID first; for rules without an ID, we fall back to
// positional alignment when the result and preserve lists have the same
// length. The positional fallback only triggers when both sides are null at
// position i (i.e. neither the prior model nor the API response identifies
// the rule by ID), so it cannot accidentally cross-pollinate values between
// distinguishable rules.
func flattenLifecycleRules(in []s3types.LifecycleRule, preserve []LifecycleRuleModel) []LifecycleRuleModel {
	preservedPrefix := make(map[string]types.String, len(preserve))
	for _, p := range preserve {
		if !isMissingRuleID(p.ID) {
			preservedPrefix[p.ID.ValueString()] = p.Prefix
		}
	}
	sameLength := len(in) == len(preserve)

	out := make([]LifecycleRuleModel, 0, len(in))
	for i, r := range in {
		mdl := LifecycleRuleModel{
			ID:     types.StringPointerValue(r.ID),
			Prefix: types.StringNull(),
			Status: types.StringValue(string(r.Status)),
		}
		if r.ID != nil && *r.ID != "" {
			if pref, ok := preservedPrefix[*r.ID]; ok {
				mdl.Prefix = pref
			}
		} else if sameLength && isMissingRuleID(preserve[i].ID) {
			// Both sides have no ID at this position — positional alignment is
			// the only signal we have, and it's safe because there's no other
			// rule the preserved value could refer to.
			mdl.Prefix = preserve[i].Prefix
		}

		if r.Expiration != nil {
			expiration := &ExpirationModel{
				ExpiredObjectDeleteMarker: types.BoolPointerValue(r.Expiration.ExpiredObjectDeleteMarker),
				Days:                      types.Int32PointerValue(r.Expiration.Days),
			}

			if r.Expiration.Date != nil {
				expiration.Date = types.StringValue(r.Expiration.Date.Format(time.RFC3339))
			} else {
				expiration.Date = types.StringNull()
			}

			mdl.Expiration = expiration
		}

		for _, t := range r.Transitions {
			transition := &TransitionModel{
				Date:         types.StringNull(),
				Days:         types.Int32PointerValue(t.Days),
				StorageClass: types.StringValue(string(t.StorageClass)),
			}
			if t.Date != nil {
				transition.Date = types.StringValue(t.Date.Format(time.RFC3339))
			}
			mdl.Transitions = append(mdl.Transitions, transition)
		}

		if r.NoncurrentVersionExpiration != nil {
			mdl.NoncurrentVersionExpiration = &NoncurrentVersionExpirationModel{
				NoncurrentDays:          types.Int32PointerValue(r.NoncurrentVersionExpiration.NoncurrentDays),
				NewerNoncurrentVersions: types.Int32PointerValue(r.NoncurrentVersionExpiration.NewerNoncurrentVersions),
			}
		}

		for _, nct := range r.NoncurrentVersionTransitions {
			ncTransition := &NoncurrentVersionTransitionModel{
				NoncurrentDays:          types.Int32PointerValue(nct.NoncurrentDays),
				StorageClass:            types.StringValue(string(nct.StorageClass)),
				NewerNoncurrentVersions: types.Int32PointerValue(nct.NewerNoncurrentVersions),
			}
			mdl.NoncurrentVersionTransitions = append(mdl.NoncurrentVersionTransitions, ncTransition)
		}

		if r.AbortIncompleteMultipartUpload != nil {
			mdl.AbortIncompleteMultipart = &AbortIncompleteMultipartModel{
				DaysAfterInitiation: types.Int32PointerValue(r.AbortIncompleteMultipartUpload.DaysAfterInitiation),
			}
		}

		if r.Filter != nil {
			f := FilterModel{
				Prefix: types.StringPointerValue(r.Filter.Prefix),
			}
			if r.Filter.Tag != nil {
				f.Tag = &TagModel{
					Key:   types.StringPointerValue(r.Filter.Tag.Key),
					Value: types.StringPointerValue(r.Filter.Tag.Value),
				}
			}
			f.ObjectSizeGreaterThan = types.Int64PointerValue(r.Filter.ObjectSizeGreaterThan)
			f.ObjectSizeLessThan = types.Int64PointerValue(r.Filter.ObjectSizeLessThan)
			if r.Filter.And != nil {
				and := AndFilterModel{
					Prefix:                types.StringPointerValue(r.Filter.And.Prefix),
					ObjectSizeGreaterThan: types.Int64PointerValue(r.Filter.And.ObjectSizeGreaterThan),
					ObjectSizeLessThan:    types.Int64PointerValue(r.Filter.And.ObjectSizeLessThan),
				}
				andTags := map[string]attr.Value{}
				for _, t := range r.Filter.And.Tags {
					andTags[*t.Key] = types.StringPointerValue(t.Value)
				}
				and.Tags = types.MapNull(types.StringType)
				if len(andTags) > 0 {
					tagMapValue, _ := types.MapValue(types.StringType, andTags)
					and.Tags = tagMapValue
				}

				f.And = &and
			}
			mdl.Filter = &f
		}

		out = append(out, mdl)
	}
	return out
}

func isMissingRuleID(id types.String) bool {
	return id.IsNull() || id.IsUnknown() || id.ValueString() == ""
}

func (r *BucketInventoryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data BucketLifecycleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := r.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	out, err := s3c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == ErrNoSuchLifecycleConfiguration) {
			resp.State.RemoveResource(ctx)
			return
		}
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	// use our helper to flatten
	data.Rule = flattenLifecycleRules(out.Rules, data.Rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *BucketInventoryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data BucketLifecycleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := r.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	rules := expandRules(ctx, data.Rule)
	lifecycleConfig := &s3types.BucketLifecycleConfiguration{
		Rules: rules,
	}
	_, err = s3c.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
		Bucket:                 aws.String(data.Bucket.ValueString()),
		LifecycleConfiguration: lifecycleConfig,
	})
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	// set state while we wait for the lifecycle configuration to propagate
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// wait for lifecycle config  to be read back from s3 API since it is not guaranteed to propagate immediately
	if result, err := waitForLifecycleConfig(ctx, s3c, data.Bucket.ValueString(), *lifecycleConfig); err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	} else {
		// Read the result back into state. Terraform will detect and fail if the state does not match the plan.
		data.Rule = flattenLifecycleRules(result.Rules, data.Rule)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *BucketInventoryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BucketLifecycleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := r.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	_, err = s3c.DeleteBucketLifecycle(ctx, &s3.DeleteBucketLifecycleInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == ErrNoSuchLifecycleConfiguration) || (apiErr.ErrorCode() == ErrNoSuchBucket) {
			// bucket lifecycle config doesn’t exist, return as it will be removed from state
			return
		}

		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
	}
}

func (r *BucketInventoryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	data := BucketLifecycleResourceModel{
		Bucket: types.StringValue(req.ID),
	}

	s3c, err := r.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}
	out, err := s3c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	// use our helper to flatten
	data.Rule = flattenLifecycleRules(out.Rules, data.Rule)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// MustRenderBucketLifecycleConfigurationResource renders HCL for a lifecycle config.
func MustRenderBucketLifecycleConfigurationResource(ctx context.Context, name string, cfg *BucketLifecycleResourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	// resource block
	block := body.AppendNewBlock(
		"resource",
		[]string{"coreweave_object_storage_bucket_lifecycle_configuration", name},
	)
	b := block.Body()

	// bucket attribute
	b.SetAttributeRaw("bucket", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(cfg.Bucket.ValueString())}})

	// rule blocks
	for _, rule := range cfg.Rule {
		rb := b.AppendNewBlock("rule", nil).Body()

		// top‐level attrs
		if !rule.ID.IsNull() {
			rb.SetAttributeValue("id", cty.StringVal(rule.ID.ValueString()))
		}
		if !rule.Prefix.IsNull() {
			rb.SetAttributeValue("prefix", cty.StringVal(rule.Prefix.ValueString()))
		}
		rb.SetAttributeValue("status", cty.StringVal(rule.Status.ValueString()))

		// abort_incomplete_multipart_upload
		if rule.AbortIncompleteMultipart != nil {
			aim := rb.AppendNewBlock("abort_incomplete_multipart_upload", nil).Body()
			aim.SetAttributeValue("days_after_initiation",
				cty.NumberIntVal(int64(rule.AbortIncompleteMultipart.DaysAfterInitiation.ValueInt32())),
			)
		}

		// expiration
		if rule.Expiration != nil {
			exp := rb.AppendNewBlock("expiration", nil).Body()
			if !rule.Expiration.Date.IsNull() {
				exp.SetAttributeValue("date", cty.StringVal(rule.Expiration.Date.ValueString()))
			}
			if !rule.Expiration.Days.IsNull() {
				exp.SetAttributeValue("days",
					cty.NumberIntVal(int64(rule.Expiration.Days.ValueInt32())),
				)
			}
			if !rule.Expiration.ExpiredObjectDeleteMarker.IsNull() {
				exp.SetAttributeValue("expired_object_delete_marker",
					cty.BoolVal(rule.Expiration.ExpiredObjectDeleteMarker.ValueBool()),
				)
			}
		}

		// noncurrent_version_expiration
		if rule.NoncurrentVersionExpiration != nil {
			nc := rb.AppendNewBlock("noncurrent_version_expiration", nil).Body()
			if !rule.NoncurrentVersionExpiration.NoncurrentDays.IsNull() {
				nc.SetAttributeValue("noncurrent_days",
					cty.NumberIntVal(int64(rule.NoncurrentVersionExpiration.NoncurrentDays.ValueInt32())),
				)
			}
			if !rule.NoncurrentVersionExpiration.NewerNoncurrentVersions.IsNull() {
				nc.SetAttributeValue("newer_noncurrent_versions",
					cty.NumberIntVal(int64(rule.NoncurrentVersionExpiration.NewerNoncurrentVersions.ValueInt32())),
				)
			}
		}

		// filter
		if rule.Filter != nil {
			fb := rb.AppendNewBlock("filter", nil).Body()

			// filter attrs
			if !rule.Filter.Prefix.IsNull() {
				fb.SetAttributeValue("prefix", cty.StringVal(rule.Filter.Prefix.ValueString()))
			}
			if !rule.Filter.ObjectSizeGreaterThan.IsNull() {
				fb.SetAttributeValue("object_size_greater_than",
					cty.NumberIntVal(rule.Filter.ObjectSizeGreaterThan.ValueInt64()),
				)
			}
			if !rule.Filter.ObjectSizeLessThan.IsNull() {
				fb.SetAttributeValue("object_size_less_than",
					cty.NumberIntVal(rule.Filter.ObjectSizeLessThan.ValueInt64()),
				)
			}

			// tag block
			if rule.Filter.Tag != nil {
				tag := fb.AppendNewBlock("tag", nil).Body()
				tag.SetAttributeValue("key", cty.StringVal(rule.Filter.Tag.Key.ValueString()))
				tag.SetAttributeValue("value", cty.StringVal(rule.Filter.Tag.Value.ValueString()))
			}

			// and block
			if rule.Filter.And != nil {
				and := fb.AppendNewBlock("and", nil).Body()

				if !rule.Filter.And.Prefix.IsNull() {
					and.SetAttributeValue("prefix", cty.StringVal(rule.Filter.And.Prefix.ValueString()))
				}

				// tags as a map
				if !rule.Filter.And.Tags.IsNull() {
					var tagMap map[string]string
					rule.Filter.And.Tags.ElementsAs(ctx, &tagMap, false)
					// convert to cty map
					attrs := make(map[string]cty.Value, len(tagMap))
					for k, v := range tagMap {
						attrs[k] = cty.StringVal(v)
					}
					and.SetAttributeValue("tags", cty.MapVal(attrs))
				}

				if !rule.Filter.And.ObjectSizeGreaterThan.IsNull() {
					and.SetAttributeValue("object_size_greater_than",
						cty.NumberIntVal(rule.Filter.And.ObjectSizeGreaterThan.ValueInt64()),
					)
				}
				if !rule.Filter.And.ObjectSizeLessThan.IsNull() {
					and.SetAttributeValue("object_size_less_than",
						cty.NumberIntVal(rule.Filter.And.ObjectSizeLessThan.ValueInt64()),
					)
				}
			}
		}
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

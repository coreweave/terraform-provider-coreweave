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

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework-validators/int32validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/zclconf/go-cty/cty"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &BucketLifecycleResource{}
	_ resource.ResourceWithImportState = &BucketLifecycleResource{}
)

const (
	ErrNoSuchLifecycleConfiguration string = "NoSuchLifecycleConfiguration"
)

// NewBucketLifecycleResource returns a new resource instance.
func NewBucketLifecycleResource() resource.Resource {
	return &BucketLifecycleResource{}
}

// BucketLifecycleResource is the resource implementation.
type BucketLifecycleResource struct {
	client *coreweave.Client
}

// BucketLifecycleResourceModel maps resource schema data.
type BucketLifecycleResourceModel struct {
	Bucket types.String         `tfsdk:"bucket"`
	Rule   []LifecycleRuleModel `tfsdk:"rule"`
}

// LifecycleRuleModel maps a single lifecycle rule block.
type LifecycleRuleModel struct {
	ID                           types.String                        `tfsdk:"id"`
	Prefix                       types.String                        `tfsdk:"prefix"`
	Status                       types.String                        `tfsdk:"status"`
	Expiration                   *ExpirationModel                    `tfsdk:"expiration"`
	Transitions                  []*TransitionModel                  `tfsdk:"transition"`
	NoncurrentVersionExpiration  *NoncurrentVersionExpirationModel   `tfsdk:"noncurrent_version_expiration"`
	NoncurrentVersionTransitions []*NoncurrentVersionTransitionModel `tfsdk:"noncurrent_version_transition"`
	AbortIncompleteMultipart     *AbortIncompleteMultipartModel      `tfsdk:"abort_incomplete_multipart_upload"`
	Filter                       *FilterModel                        `tfsdk:"filter"`
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

// FilterModel maps the filter sub-block.
type FilterModel struct {
	Prefix                types.String    `tfsdk:"prefix"`
	Tag                   *TagModel       `tfsdk:"tag"`
	ObjectSizeGreaterThan types.Int64     `tfsdk:"object_size_greater_than"`
	ObjectSizeLessThan    types.Int64     `tfsdk:"object_size_less_than"`
	And                   *AndFilterModel `tfsdk:"and"`
}

func (r *BucketLifecycleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage_bucket_lifecycle_configuration"
}

func (r *BucketLifecycleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Object Storage Bucket Lifecycle Configuration",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the bucket to apply lifecycle configuration to",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
		Blocks: map[string]schema.Block{
			"rule": schema.ListNestedBlock{
				MarkdownDescription: "One or more lifecycle rule blocks",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Unique identifier for the rule",
							PlanModifiers: []planmodifier.String{
								stringplanmodifier.UseStateForUnknown(),
							},
						},
						"prefix": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Object key prefix to which the rule applies",
						},
						"status": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Rule status: Enabled or Disabled",
						},
					},
					Blocks: map[string]schema.Block{
						"abort_incomplete_multipart_upload": schema.SingleNestedBlock{
							Attributes: map[string]schema.Attribute{
								"days_after_initiation": schema.Int32Attribute{
									Optional:            true,
									MarkdownDescription: "Days after initiation to abort multipart uploads",
								},
							},
						},
						"expiration": schema.SingleNestedBlock{
							Attributes: map[string]schema.Attribute{
								"date": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "ISO8601 date when objects expire",
								},
								"days": schema.Int32Attribute{
									Optional:            true,
									Validators:          []validator.Int32{int32validator.AtLeast(0)},
									MarkdownDescription: "Number of days after object creation for expiration",
								},
								"expired_object_delete_marker": schema.BoolAttribute{
									Optional:            true,
									MarkdownDescription: "Whether to remove expired delete markers",
								},
							},
						},
						"filter": schema.SingleNestedBlock{
							Blocks: map[string]schema.Block{
								"tag": schema.SingleNestedBlock{
									Attributes: map[string]schema.Attribute{
										"key": schema.StringAttribute{
											Optional:            true,
											MarkdownDescription: "Tag key filter",
										},
										"value": schema.StringAttribute{
											Optional:            true,
											MarkdownDescription: "Tag value filter",
										},
									},
								},
								"and": schema.SingleNestedBlock{
									MarkdownDescription: "Configuration block used to apply a logical AND to two or more predicates. The Lifecycle Rule will apply to any object matching all the predicates configured inside the and block.",
									Attributes: map[string]schema.Attribute{
										"prefix": schema.StringAttribute{
											Optional:            true,
											MarkdownDescription: "Prefix identifying one or more objects to which the rule applies.",
										},
										"tags": schema.MapAttribute{
											Optional:            true,
											MarkdownDescription: "Map for specifying tag keys and values.",
											ElementType:         types.StringType,
										},
										"object_size_greater_than": schema.Int64Attribute{
											Optional:            true,
											MarkdownDescription: "Minimum object size (in bytes) to which the rule applies.",
										},
										"object_size_less_than": schema.Int64Attribute{
											Optional:            true,
											MarkdownDescription: "Maximum object size (in bytes) to which the rule applies.",
										},
									},
								},
							},
							Attributes: map[string]schema.Attribute{
								"prefix": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "Prefix filter",
								},
								"object_size_greater_than": schema.Int64Attribute{
									Optional:            true,
									MarkdownDescription: "Minimum object size (in bytes) to which the rule applies.",
								},
								"object_size_less_than": schema.Int64Attribute{
									Optional:            true,
									MarkdownDescription: "Maximum object size (in bytes) to which the rule applies.",
								},
							},
						},
						"noncurrent_version_expiration": schema.SingleNestedBlock{
							Attributes: map[string]schema.Attribute{
								"noncurrent_days": schema.Int32Attribute{
									Optional:            true,
									MarkdownDescription: "Days after becoming noncurrent before deletion",
								},
								"newer_noncurrent_versions": schema.Int32Attribute{
									Optional:            true,
									MarkdownDescription: "Number of noncurrent versions to retain",
								},
							},
						},
						"noncurrent_version_transition": schema.SetNestedBlock{
							NestedObject: schema.NestedBlockObject{
								Attributes: map[string]schema.Attribute{
									"newer_noncurrent_versions": schema.Int32Attribute{
										Optional:            true,
										Validators:          []validator.Int32{int32validator.AtLeast(1)},
										MarkdownDescription: "Number of noncurrent versions to retain",
									},
									"noncurrent_days": schema.Int32Attribute{
										Required:            true,
										Validators:          []validator.Int32{int32validator.AtLeast(0)},
										MarkdownDescription: "Number of days after object becomes noncurrent before the transition may occur",
									},
									"storage_class": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "Storage class to transition noncurrent objects to",
									},
								},
							},
						},
						"transition": schema.SetNestedBlock{
							NestedObject: schema.NestedBlockObject{
								Attributes: map[string]schema.Attribute{
									"date": schema.StringAttribute{
										Optional: true,
										Validators: []validator.String{
											stringvalidator.ConflictsWith(path.MatchRelative().AtParent().AtName("days")),
										},
										MarkdownDescription: "ISO8601 date when objects transition",
									},
									"days": schema.Int32Attribute{
										Optional: true,
										Validators: []validator.Int32{
											int32validator.ConflictsWith(path.MatchRelative().AtParent().AtName("date")),
											int32validator.AtLeast(0),
										},
										MarkdownDescription: "Number of days after object creation for transition",
									},
									"storage_class": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "Storage class to transition objects to",
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *BucketLifecycleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func expandRules(ctx context.Context, in []LifecycleRuleModel) []s3types.LifecycleRule {
	out := make([]s3types.LifecycleRule, 0, len(in))
	for _, r := range in {
		rule := s3types.LifecycleRule{
			ID:     aws.String(r.ID.ValueString()),
			Status: s3types.ExpirationStatus(r.Status.ValueString()),
		}
		if !r.Prefix.IsNull() {
			rule.Prefix = aws.String(r.Prefix.ValueString()) //nolint:staticcheck
		}
		if r.Expiration != nil {
			exp := s3types.LifecycleExpiration{}
			if !r.Expiration.Date.IsNull() {
				exp.Date = aws.Time(parseISO8601(r.Expiration.Date.ValueString()))
			}
			if !r.Expiration.Days.IsNull() {
				exp.Days = aws.Int32(r.Expiration.Days.ValueInt32())
			}
			if !r.Expiration.ExpiredObjectDeleteMarker.IsNull() {
				exp.ExpiredObjectDeleteMarker = aws.Bool(r.Expiration.ExpiredObjectDeleteMarker.ValueBool())
			}
			rule.Expiration = &exp
		}
		for _, transition := range r.Transitions {
			t := s3types.Transition{
				StorageClass: s3types.TransitionStorageClass(transition.StorageClass.ValueString()),
				Days:         transition.Days.ValueInt32Pointer(),
			}
			if !transition.Date.IsNull() {
				t.Date = aws.Time(parseISO8601(transition.Date.ValueString()))
			}
			rule.Transitions = append(rule.Transitions, t)
		}
		if r.NoncurrentVersionExpiration != nil {
			nc := s3types.NoncurrentVersionExpiration{}
			if !r.NoncurrentVersionExpiration.NoncurrentDays.IsNull() {
				nc.NoncurrentDays = aws.Int32(r.NoncurrentVersionExpiration.NoncurrentDays.ValueInt32())
			}
			if !r.NoncurrentVersionExpiration.NewerNoncurrentVersions.IsNull() {
				nc.NewerNoncurrentVersions = aws.Int32(r.NoncurrentVersionExpiration.NewerNoncurrentVersions.ValueInt32())
			}
			rule.NoncurrentVersionExpiration = &nc
		}
		if r.AbortIncompleteMultipart != nil {
			ai := s3types.AbortIncompleteMultipartUpload{
				DaysAfterInitiation: aws.Int32(r.AbortIncompleteMultipart.DaysAfterInitiation.ValueInt32()),
			}
			rule.AbortIncompleteMultipartUpload = &ai
		}
		if r.Filter != nil {
			f := s3types.LifecycleRuleFilter{}
			if !r.Filter.Prefix.IsNull() {
				f.Prefix = aws.String(r.Filter.Prefix.ValueString())
			}
			if r.Filter.Tag != nil {
				f.Tag = &s3types.Tag{
					Key:   aws.String(r.Filter.Tag.Key.ValueString()),
					Value: aws.String(r.Filter.Tag.Value.ValueString()),
				}
			}
			if r.Filter.ObjectSizeGreaterThan.ValueInt64() > 0 {
				f.ObjectSizeGreaterThan = aws.Int64(r.Filter.ObjectSizeGreaterThan.ValueInt64())
			}

			if r.Filter.ObjectSizeLessThan.ValueInt64() > 0 {
				f.ObjectSizeLessThan = aws.Int64(r.Filter.ObjectSizeLessThan.ValueInt64())
			}

			if r.Filter.And != nil {
				and := s3types.LifecycleRuleAndOperator{}
				if !r.Filter.And.Prefix.IsNull() {
					and.Prefix = aws.String(r.Filter.And.Prefix.ValueString())
				}
				andTags := map[string]string{}
				r.Filter.And.Tags.ElementsAs(ctx, &andTags, false)
				if len(andTags) > 0 {
					for key, value := range andTags {
						and.Tags = append(and.Tags, s3types.Tag{
							Key:   aws.String(key),
							Value: aws.String(value),
						})
					}
				}
				if r.Filter.And.ObjectSizeGreaterThan.ValueInt64() > 0 {
					and.ObjectSizeGreaterThan = aws.Int64(r.Filter.And.ObjectSizeGreaterThan.ValueInt64())
				}

				if r.Filter.And.ObjectSizeLessThan.ValueInt64() > 0 {
					and.ObjectSizeLessThan = aws.Int64(r.Filter.And.ObjectSizeLessThan.ValueInt64())
				}
				f.And = &and
			}
			rule.Filter = &f
		}
		out = append(out, rule)
	}

	return out
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

// eqLifecycleRule compares two LifecycleRule objects for exact equality.
func eqLifecycleRule(a, b s3types.LifecycleRule) bool { //nolint:gocyclo
	// Compare simple scalar fields via aws.ToString / string conversion
	if aws.ToString(a.ID) != aws.ToString(b.ID) {
		return false
	}
	if aws.ToString(a.Prefix) != aws.ToString(b.Prefix) { //nolint:staticcheck
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

func waitForLifecycleConfig(parentCtx context.Context, client *s3.Client, bucket string, expected s3types.BucketLifecycleConfiguration) error {
	// make a sorted copy of expected rules
	exp := append([]s3types.LifecycleRule(nil), expected.Rules...)
	slices.SortFunc(exp, cmpLifecycleRule)

	return coreweave.PollUntil("bucket lifecycle configuration", parentCtx, 5*time.Second, 5*time.Minute, func(ctx context.Context) (bool, error) {
		out, err := client.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(bucket)})
		if err != nil {
			if isTransientS3Error(err) {
				return false, nil
			}
			return false, err
		}

		rules := out.Rules
		slices.SortFunc(rules, cmpLifecycleRule)
		return slices.EqualFunc(exp, rules, eqLifecycleRule), nil
	})
}

func (r *BucketLifecycleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
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
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		resp.Diagnostics.AddError("Failed to marshal lifecycle rules to JSON", err.Error())
		return
	}
	tflog.Debug(ctx, "creating lifecycle rules for bucket", map[string]any{"rules": string(rulesJSON), "bucket": data.Bucket.ValueString()})
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
	if err := waitForLifecycleConfig(ctx, s3c, data.Bucket.ValueString(), *lifecycleConfig); err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// flattenLifecycleRules turns AWS SDK LifecycleRule objects into our Terraform model.
func flattenLifecycleRules(in []s3types.LifecycleRule) []LifecycleRuleModel {
	out := make([]LifecycleRuleModel, 0, len(in))
	for _, r := range in {
		mdl := LifecycleRuleModel{
			ID:     types.StringPointerValue(r.ID),
			Prefix: types.StringPointerValue(r.Prefix), //nolint:staticcheck
			Status: types.StringValue(string(r.Status)),
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
					Prefix:                types.StringValue(aws.ToString(r.Filter.And.Prefix)),
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

func (r *BucketLifecycleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
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
	data.Rule = flattenLifecycleRules(out.Rules)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *BucketLifecycleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
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
	if err := waitForLifecycleConfig(ctx, s3c, data.Bucket.ValueString(), *lifecycleConfig); err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *BucketLifecycleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
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

func (r *BucketLifecycleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
	data.Rule = flattenLifecycleRules(out.Rules)

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

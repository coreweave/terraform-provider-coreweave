package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/transport/http"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/zclconf/go-cty/cty"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &BucketResource{}
	_ resource.ResourceWithImportState = &BucketResource{}
)

const (
	ErrNoSuchBucket string = "NoSuchBucket"
)

func NewBucketResource() resource.Resource {
	return &BucketResource{}
}

// BucketResource defines the resource implementation.
type BucketResource struct {
	client *coreweave.Client
}

type BucketResourceModel struct {
	Name types.String `tfsdk:"name"`
	Zone types.String `tfsdk:"zone"`
	Tags types.Map    `tfsdk:"tags"`
}

func (b *BucketResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage_bucket"
}

func (b *BucketResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Buckets are the primary organizational containers for your data in CoreWeave AI Object Storage. Bucket names must be globally-unique and not begin with `cw-` or `vip-`, which are reserved for internal use. Learn more about [creating buckets](https://docs.coreweave.com/products/storage/object-storage/buckets/create-bucket).",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the bucket, must be unique",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"zone": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The Availability Zone in which the bucket is located.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tags": schema.MapAttribute{
				Optional:            true,
				MarkdownDescription: "Map of tags to assign to the bucket.",
				ElementType:         types.StringType,
			},
		},
	}
}

func (b *BucketResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
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

func handleS3Error(
	err error,
	diags *diag.Diagnostics,
	bucketName string,
) {
	// 1) OperationError ⇒ report service, operation, inner error
	var opErr *smithy.OperationError
	if errors.As(err, &opErr) {
		diags.AddError(
			fmt.Sprintf("S3 %s failed on bucket %q", opErr.OperationName, bucketName),
			fmt.Sprintf("service=%s operation=%s: %v",
				opErr.ServiceID, opErr.OperationName, opErr.Err,
			),
		)
		return
	}

	// 2) APIError (interface) ⇒ report code/message/fault
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		diags.AddError(
			fmt.Sprintf("Error interacting with bucket %q", bucketName),
			fmt.Sprintf("%s: %s (fault=%s)",
				apiErr.ErrorCode(), apiErr.ErrorMessage(), apiErr.ErrorFault(),
			),
		)
		return
	}

	// 3) URL/transport errors
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		diags.AddError(
			fmt.Sprintf("Network error calling bucket %q", bucketName),
			fmt.Sprintf("%s %s: %v",
				urlErr.Op, urlErr.URL, urlErr.Err,
			),
		)
		return
	}

	// 4) Fallback
	diags.AddError(
		fmt.Sprintf("Unexpected error with bucket %q", bucketName),
		err.Error(),
	)
}

// waitForBucket polls HeadBucket every 'interval' until the bucket
// either exists (shouldExist=true) or is deleted (shouldExist=false),
// or the context times out/cancels.
//
// Returns nil on the desired state; any other error (including timeout)
// is returned directly.
func waitForBucket(parentCtx context.Context, client *s3.Client, bucket string, shouldExist bool) error {
	operation := "bucket creation"
	if !shouldExist {
		operation = "bucket deletion"
	}
	return coreweave.PollUntil(operation, parentCtx, 5*time.Second, 5*time.Minute, func(ctx context.Context) (bool, error) {
		_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})

		// desired state: exists
		if shouldExist {
			if err == nil {
				return true, nil
			}
			// retry on “not found” or “bad request”
			var httpErr *http.ResponseError
			if errors.As(err, &httpErr) &&
				(httpErr.Response.StatusCode == 400 || httpErr.Response.StatusCode == 404) {
				return false, nil
			}
			return false, err
		}

		// desired state: deleted
		if err != nil {
			var httpErr *http.ResponseError
			if errors.As(err, &httpErr) &&
				(httpErr.Response.StatusCode == 400 || httpErr.Response.StatusCode == 404) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
}

// cmpTag returns -1 if a<b, +1 if a>b, or 0 if equal (nil-safe).
func cmpTag(a, b s3types.Tag) int {
	// Key
	switch {
	case a.Key == nil && b.Key != nil:
		return -1
	case a.Key != nil && b.Key == nil:
		return 1
	case a.Key != nil && b.Key != nil:
		if *a.Key < *b.Key {
			return -1
		} else if *a.Key > *b.Key {
			return 1
		}
	}
	// Value
	switch {
	case a.Value == nil && b.Value != nil:
		return -1
	case a.Value != nil && b.Value == nil:
		return 1
	case a.Value != nil && b.Value != nil:
		if *a.Value < *b.Value {
			return -1
		} else if *a.Value > *b.Value {
			return 1
		}
	}
	return 0
}

// eqTag returns true if tags are identical (nil-safe).
func eqTag(x, y s3types.Tag) bool {
	var eqPtr = func(a, b *string) bool {
		if a == b {
			return true
		}
		if a == nil || b == nil {
			return false
		}
		return *a == *b
	}
	return eqPtr(x.Key, y.Key) && eqPtr(x.Value, y.Value)
}

func waitForBucketTags(parentCtx context.Context, client *s3.Client, bucket string, expected []s3types.Tag) error {
	// make a sorted copy of expected
	exp := append([]s3types.Tag(nil), expected...)
	slices.SortFunc(exp, cmpTag)

	return coreweave.PollUntil("bucket tag propagation", parentCtx, 5*time.Second, 5*time.Minute, func(ctx context.Context) (bool, error) {
		out, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(bucket)})
		if err != nil {
			if isTransientS3Error(err) {
				return false, nil
			}
			return false, err
		}
		tags := out.TagSet
		slices.SortFunc(tags, cmpTag)
		return slices.EqualFunc(exp, tags, eqTag), nil
	})
}

func (b *BucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data BucketResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	s3Client, err := b.client.S3Client(ctx, data.Zone.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	// check if bucket already exists - we need to HeadBucket here because the cwobject API only errors
	// if you try to create a bucket with the same name in a different zone.
	// If a CreateBucket request is sent for a name/zone combo that already exists, it will succeed.
	// So we HeadBucket here and check if the request succeeds; if it does, error and tell the user the bucket already exists
	// so they can import the existing state
	_, err = s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(data.Name.ValueString()),
	})
	if err == nil {
		resp.Diagnostics.AddError(
			"Already Exists",
			fmt.Sprintf("Bucket '%s' already exists, specify a different name and try again, or import the bucket using `terraform import`.", data.Name.ValueString()),
		)
		return
	}

	createReq := &s3.CreateBucketInput{
		Bucket: aws.String(data.Name.ValueString()),
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(data.Zone.ValueString()),
		},
	}

	_, err = s3Client.CreateBucket(ctx, createReq)
	if err != nil {
		// These two error types are only returned in a situation where a user
		// tries to create a bucket with the same name but a different zone.
		// The HeadBucket call before CreateBucket should ensure that Terraform will error on
		// a bucket that already exists in the same zone
		var bucketExistsErr *s3types.BucketAlreadyExists
		var bucketOwnedByYouErr *s3types.BucketAlreadyOwnedByYou
		if errors.As(err, &bucketExistsErr) || errors.As(err, &bucketOwnedByYouErr) {
			// Bucket was already created in a previous attempt, return an error
			message := bucketExistsErr.Message
			if message == nil {
				message = bucketOwnedByYouErr.Message
			}

			resp.Diagnostics.AddError(
				"Already Exists",
				fmt.Sprintf("Bucket '%s' already exists, specify a different name and try again, or import the bucket using `terraform import`: %s", data.Name.ValueString(), *message),
			)
			return
		}

		handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
		return
	}

	// set state while we wait for the bucket to finish
	if diag := resp.State.Set(ctx, &data); diag.HasError() {
		// if we fail to set state, return early as the resource will be orphaned
		resp.Diagnostics.Append(diag...)
		return
	}

	if err := waitForBucket(ctx, s3Client, data.Name.ValueString(), true); err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
		return
	}

	if !data.Tags.IsNull() {
		tags := []s3types.Tag{}
		tagMap := map[string]string{}

		if diag := data.Tags.ElementsAs(ctx, &tagMap, false); diag.HasError() {
			detail := diag.Errors()[0].Detail()
			resp.Diagnostics.AddError("Invalid S3 Bucket Tags", detail)
			return
		}

		for key, value := range tagMap {
			tags = append(tags, s3types.Tag{
				Key:   aws.String(key),
				Value: aws.String(value),
			})
		}

		_, err = s3Client.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
			Bucket: data.Name.ValueStringPointer(),
			Tagging: &s3types.Tagging{
				TagSet: tags,
			},
		})
		if err != nil {
			handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
			return
		}

		// set state while we wait for the tags to propagate
		if diag := resp.State.Set(ctx, &data); diag.HasError() {
			// if we fail to set state, return early as the resource will be orphaned
			resp.Diagnostics.Append(diag...)
			return
		}

		if err := waitForBucketTags(ctx, s3Client, data.Name.ValueString(), tags); err != nil {
			handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
			return
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (b *BucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data BucketResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	s3Client, err := b.client.S3Client(ctx, data.Zone.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	tagSet, err := s3Client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
		Bucket: aws.String(data.Name.ValueString()),
	})
	if err != nil {
		var httpErr *http.ResponseError
		if errors.As(err, &httpErr) && httpErr.Response != nil {
			// if we get a 404 back from the client, the bucket does not exist & can be removed from state
			if httpErr.Response.StatusCode == 404 {
				resp.State.RemoveResource(ctx)
				return
			}
		}

		handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
		return
	}

	location, err := s3Client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String(data.Name.ValueString()),
	})
	if err != nil {
		var httpErr *http.ResponseError
		if errors.As(err, &httpErr) && httpErr.Response != nil {
			// if we get a 404 back from the client, the bucket does not exist & can be removed from state
			if httpErr.Response.StatusCode == 404 {
				resp.State.RemoveResource(ctx)
				return
			}
		}

		handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
		return
	}

	tags := types.MapNull(types.StringType)
	if len(tagSet.TagSet) > 0 {
		tagMap := map[string]attr.Value{}
		for _, t := range tagSet.TagSet {
			tagMap[*t.Key] = types.StringValue(*t.Value)
		}
		tagMapValue, diag := types.MapValue(types.StringType, tagMap)
		resp.Diagnostics.Append(diag...)
		if resp.Diagnostics.HasError() {
			return
		}

		tags = tagMapValue
	}

	data.Zone = types.StringValue(string(location.LocationConstraint))
	data.Tags = tags
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (b *BucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data BucketResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	s3Client, err := b.client.S3Client(ctx, data.Zone.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	if !data.Tags.IsNull() {
		tags := []s3types.Tag{}
		tagMap := map[string]string{}

		if diag := data.Tags.ElementsAs(ctx, &tagMap, false); diag.HasError() {
			resp.Diagnostics.Append(diag...)
			return
		}

		for key, value := range tagMap {
			tags = append(tags, s3types.Tag{
				Key:   aws.String(key),
				Value: aws.String(value),
			})
		}
		_, err = s3Client.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
			Bucket: aws.String(data.Name.ValueString()),
			Tagging: &s3types.Tagging{
				TagSet: tags,
			},
		})
		if err != nil {
			handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
			return
		}

		if err := waitForBucketTags(ctx, s3Client, data.Name.ValueString(), tags); err != nil {
			handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
			return
		}
	} else {
		_, err = s3Client.DeleteBucketTagging(ctx, &s3.DeleteBucketTaggingInput{
			Bucket: aws.String(data.Name.ValueString()),
		})
		if err != nil {
			handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
			return
		}

		if err := waitForBucketTags(ctx, s3Client, data.Name.ValueString(), []s3types.Tag{}); err != nil {
			handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
			return
		}

		data.Tags = types.MapNull(types.StringType)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (b *BucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BucketResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	s3Client, err := b.client.S3Client(ctx, data.Zone.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	_, err = s3Client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(data.Name.ValueString()),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == ErrNoSuchBucket {
			// bucket doesn’t exist, return as it will be removed from state
			return
		}

		handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
		return
	}

	// wait for bucket to fully finish deleting
	if err := waitForBucket(ctx, s3Client, data.Name.ValueString(), false); err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
		return
	}
}

func (b *BucketResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	s3Client, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	bucket, err := s3Client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String(req.ID),
	})
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, req.ID)
		return
	}

	bucketTagging, err := s3Client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
		Bucket: aws.String(req.ID),
	})
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, req.ID)
		return
	}

	tags := types.MapNull(types.StringType)
	if len(bucketTagging.TagSet) > 0 {
		tagMap := map[string]attr.Value{}
		for _, t := range bucketTagging.TagSet {
			tagMap[*t.Key] = types.StringValue(*t.Value)
		}
		tagMapValue, diag := types.MapValue(types.StringType, tagMap)
		resp.Diagnostics.Append(diag...)
		if resp.Diagnostics.HasError() {
			return
		}

		tags = tagMapValue
	}

	data := BucketResourceModel{
		Name: types.StringValue(req.ID),
		Zone: types.StringValue(string(bucket.LocationConstraint)),
		Tags: tags,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// MustRenderBucketResource is a helper to render HCL for use in acceptance testing.
// It should not be used by clients of this library.
func MustRenderBucketResource(ctx context.Context, resourceName string, bucket *BucketResourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{"coreweave_object_storage_bucket", resourceName})
	resourceBody := resource.Body()

	resourceBody.SetAttributeValue("name", cty.StringVal(bucket.Name.ValueString()))
	resourceBody.SetAttributeValue("zone", cty.StringVal(bucket.Zone.ValueString()))

	if !bucket.Tags.IsNull() {
		tagMap := map[string]string{}
		bucket.Tags.ElementsAs(ctx, &tagMap, false)
		tags := map[string]cty.Value{}

		for key, value := range tagMap {
			tags[key] = cty.StringVal(value)
		}
		resourceBody.SetAttributeValue("tags", cty.MapVal(tags))
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

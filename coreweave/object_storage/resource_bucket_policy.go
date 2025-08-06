package objectstorage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/zclconf/go-cty/cty"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &BucketVersioningResource{}
	_ resource.ResourceWithImportState = &BucketVersioningResource{}
)

const (
	ErrNoSuchBucketPolicy string = "NoSuchBucketPolicy"
)

func NewBucketPolicyResource() resource.Resource {
	return &BucketPolicyResource{}
}

type BucketPolicyResource struct {
	client *coreweave.Client
}

type BucketPolicyResourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Policy types.String `tfsdk:"policy"`
}

func (b *BucketPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
	b.client = client
}

func (b *BucketPolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage_bucket_policy"
}

func (b *BucketPolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Object Storage Bucket Policy",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the bucket for which to apply this policy.",
			},
			"policy": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Text of the policy. Must be valid JSON. The coreweave_object_storage_bucket_policy_document data source may be used, simply reference the `.json` attribute of the data source.",
			},
		},
	}
}

func waitForBucketPolicy(parentCtx context.Context, client *s3.Client, bucket string, expected string) error {
	var expectedPolicy PolicyDocument
	if err := json.Unmarshal([]byte(expected), &expectedPolicy); err != nil {
		return err
	}

	return coreweave.PollUntil("bucket versioning configuration", parentCtx, 5*time.Second, 5*time.Minute, func(ctx context.Context) (bool, error) {
		out, err := client.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucket)})
		if err != nil {
			if isTransientS3Error(err) {
				return false, nil
			}
			return false, err
		}

		if out.Policy == nil {
			if expected == "" {
				return true, nil
			}

			return false, fmt.Errorf("bucket policy for %q was nil", bucket)
		}

		var actualPolicy PolicyDocument
		if err := json.Unmarshal([]byte(*out.Policy), &actualPolicy); err != nil {
			return false, err
		}

		return reflect.DeepEqual(expectedPolicy, actualPolicy), nil
	})
}

func (b *BucketPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data BucketPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	_, err = s3c.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: data.Bucket.ValueStringPointer(),
		Policy: data.Policy.ValueStringPointer(),
	})
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	// set state while we wait for the bucket policy to propagate
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// wait for bucket policy to be read back from s3 API since it is not guaranteed to propagate immediately
	if err := waitForBucketPolicy(ctx, s3c, data.Bucket.ValueString(), data.Policy.ValueString()); err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (b *BucketPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data BucketPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	out, err := s3c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == ErrNoSuchBucket || apiErr.ErrorCode() == ErrNoSuchBucketPolicy) {
			resp.State.RemoveResource(ctx)
			return
		}
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	// if there is no policy, remove the resource from state
	if out.Policy == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	var current PolicyDocument
	var actual PolicyDocument

	if err := json.Unmarshal([]byte(data.Policy.ValueString()), &current); err != nil {
		resp.Diagnostics.AddError("Failed to normalize S3 Bucket Policy", err.Error())
		return
	}

	if err := json.Unmarshal([]byte(*out.Policy), &actual); err != nil {
		resp.Diagnostics.AddError("Failed to normalize S3 Bucket Policy", err.Error())
		return
	}

	if !reflect.DeepEqual(current, actual) {
		data.Policy = types.StringPointerValue(out.Policy)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (b *BucketPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data BucketPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	_, err = s3c.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: data.Bucket.ValueStringPointer(),
		Policy: data.Policy.ValueStringPointer(),
	})
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	// set state while we wait for the bucket policy to propagate
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// wait for bucket policy to be read back from s3 API since it is not guaranteed to propagate immediately
	if err := waitForBucketPolicy(ctx, s3c, data.Bucket.ValueString(), data.Policy.ValueString()); err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (b *BucketPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BucketPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s3c, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}
	_, err = s3c.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == ErrNoSuchBucket || apiErr.ErrorCode() == ErrNoSuchBucketPolicy) {
			// bucket lifecycle config doesn’t exist, return as it will be removed from state
			return
		}

		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
	}
}

func (b *BucketPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	data := BucketPolicyResourceModel{
		Bucket: types.StringValue(req.ID),
	}

	s3c, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	out, err := s3c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == ErrNoSuchBucketPolicy {
			resp.State.RemoveResource(ctx)
			return
		}
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	if out.Policy == nil {
		resp.Diagnostics.AddError(fmt.Sprintf("bucket %q has no bucket policy", req.ID), "received nil bucket policy from S3")
		return
	}

	data.Policy = types.StringPointerValue(out.Policy)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// MustRenderBucketPolicyResource renders HCL for a bucket policy.
func MustRenderBucketPolicyResource(_ context.Context, name string, cfg *BucketPolicyResourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	// resource block
	block := body.AppendNewBlock(
		"resource",
		[]string{"coreweave_object_storage_bucket_policy", name},
	)
	b := block.Body()

	// bucket attribute
	b.SetAttributeRaw("bucket", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(cfg.Bucket.ValueString())}})
	b.SetAttributeValue("policy", cty.StringVal(cfg.Policy.ValueString()))

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

// MustRenderBucketPolicyResourceWithDataSource renders HCL for a bucket policy assuming the policy argument is a reference to a data source
func MustRenderBucketPolicyResourceWithDataSource(_ context.Context, name string, cfg *BucketPolicyResourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	// resource block
	block := body.AppendNewBlock(
		"resource",
		[]string{"coreweave_object_storage_bucket_policy", name},
	)
	b := block.Body()

	// bucket attribute
	b.SetAttributeRaw("bucket", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(cfg.Bucket.ValueString())}})
	b.SetAttributeRaw("policy", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(cfg.Policy.ValueString())}})

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

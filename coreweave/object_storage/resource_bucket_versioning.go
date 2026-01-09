package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &BucketVersioningResource{}
	_ resource.ResourceWithImportState = &BucketVersioningResource{}
)

func NewBucketVersioningResource() resource.Resource {
	return &BucketVersioningResource{}
}

type BucketVersioningResource struct {
	client *coreweave.Client
}

type BucketVersioningResourceModel struct {
	Bucket                  types.String                 `tfsdk:"bucket"`
	VersioningConfiguration VersioningConfigurationModel `tfsdk:"versioning_configuration"`
}

type VersioningConfigurationModel struct {
	Status types.String `tfsdk:"status"`
}

func (b *BucketVersioningResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage_bucket_versioning"
}
func (b *BucketVersioningResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Versioning protects your data by preserving all versions of objects and preventing permanent deletion. When objects are deleted, they are \"soft deleted\" with delete markers, allowing you to restore previous versions and recover data. After creating a versioned bucket with Terraform, [use `rclone` to manage versioned objects and delete markers](https://docs.coreweave.com/docs/products/storage/object-storage/buckets/rclone-versioned-buckets).",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The bucket on which to enable or suspend versioning.",
			},
		},
		Blocks: map[string]schema.Block{
			"versioning_configuration": schema.SingleNestedBlock{
				Attributes: map[string]schema.Attribute{
					"status": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Versioning state of the bucket. Valid values: Enabled, Suspended, or Disabled. Disabled should only be used when creating or importing resources that correspond to unversioned S3 buckets since the S3 API does not allow setting an Enabled/Suspended bucket to Disabled.",
					},
				},
			},
		},
	}
}

func (b *BucketVersioningResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func waitForBucketVersioning(parentCtx context.Context, client *s3.Client, bucket string, expected s3types.BucketVersioningStatus) error {
	return coreweave.PollUntil("bucket versioning configuration", parentCtx, 5*time.Second, 5*time.Minute, func(ctx context.Context) (bool, error) {
		out, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(bucket)})
		if err != nil {
			if isTransientS3Error(err) {
				return false, nil
			}
			return false, err
		}

		return out.Status == expected, nil
	})
}

func (b *BucketVersioningResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data BucketVersioningResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	s3Client, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	status := s3types.BucketVersioningStatus(data.VersioningConfiguration.Status.ValueString())
	putReq := s3.PutBucketVersioningInput{
		Bucket: aws.String(data.Bucket.ValueString()),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: status,
		},
	}
	_, err = s3Client.PutBucketVersioning(ctx, &putReq)
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// set state while we wait for the bucket versioning configuration to propagate
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// wait for bucket versioning to be read back from s3 API since it is not guaranteed to propagate immediately
	if err := waitForBucketVersioning(ctx, s3Client, data.Bucket.ValueString(), status); err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (b *BucketVersioningResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data BucketVersioningResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	s3Client, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}
	getReq := s3.GetBucketVersioningInput{
		Bucket: aws.String(data.Bucket.ValueString()),
	}
	versioning, err := s3Client.GetBucketVersioning(ctx, &getReq)
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	if versioning != nil {
		data.VersioningConfiguration.Status = types.StringValue(string(versioning.Status))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (b *BucketVersioningResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data BucketVersioningResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	s3Client, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	status := s3types.BucketVersioningStatus(data.VersioningConfiguration.Status.ValueString())
	putReq := s3.PutBucketVersioningInput{
		Bucket: aws.String(data.Bucket.ValueString()),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: status,
		},
	}
	_, err = s3Client.PutBucketVersioning(ctx, &putReq)
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// set state while we wait for the bucket versioning configuration to propagate
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// wait for bucket versioning to be read back from s3 API since it is not guaranteed to propagate immediately
	if err := waitForBucketVersioning(ctx, s3Client, data.Bucket.ValueString(), status); err != nil {
		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (b *BucketVersioningResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BucketVersioningResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	s3Client, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	// status should be set to suspended as S3 does not support flipping bucket versioning to Disable from Enabled/Suspended
	deleteReq := s3.PutBucketVersioningInput{
		Bucket: aws.String(data.Bucket.ValueString()),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusSuspended,
		},
	}

	_, err = s3Client.PutBucketVersioning(ctx, &deleteReq)
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == ErrNoSuchBucket {
			// bucket doesn’t exist, return as it will be removed from state
			return
		}

		handleS3Error(err, &resp.Diagnostics, data.Bucket.ValueString())
		return
	}
}

func (b *BucketVersioningResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	s3Client, err := b.client.S3Client(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	getReq := s3.GetBucketVersioningInput{
		Bucket: aws.String(req.ID),
	}
	versioning, err := s3Client.GetBucketVersioning(ctx, &getReq)
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, req.ID)
		return
	}

	data := BucketVersioningResourceModel{
		Bucket: types.StringValue(req.ID),
	}
	if versioning != nil {
		data.VersioningConfiguration = VersioningConfigurationModel{
			Status: types.StringValue(string(versioning.Status)),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func MustRenderBucketVersioningResource(_ context.Context, name string, bvc *BucketVersioningResourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{"coreweave_object_storage_bucket_versioning", name})
	resourceBody := resource.Body()

	// bucket attribute
	resourceBody.SetAttributeRaw("bucket", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(bvc.Bucket.ValueString())}})

	v := resourceBody.AppendNewBlock("versioning_configuration", nil).Body()
	v.SetAttributeValue("status", cty.StringVal(bvc.VersioningConfiguration.Status.ValueString()))

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}

package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/transport/http"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &BucketResource{}
	_ resource.ResourceWithImportState = &BucketResource{}
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
}

func (b *BucketResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_storage_bucket"
}

func (b *BucketResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Object Storage Bucket",
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
func waitForBucket(
	parentCtx context.Context,
	client *s3.Client,
	bucket string,
	shouldExist bool,
) error {
	timeout := 5 * time.Minute
	// derive a timeout from the parent context
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	interval := 5 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	tflog.Debug(ctx, "starting retry loop")

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for bucket %q exist=%v: %w", bucket, shouldExist, ctx.Err())
		case <-ticker.C:
			tflog.Debug(ctx, fmt.Sprintf("CLIENT REGION: %s", client.Options().Region))
			tflog.Debug(ctx, "sending head bucket")
			_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
				Bucket: aws.String(bucket),
			})

			tflog.Debug(ctx, "head bucket complete", map[string]interface{}{
				"err": err,
			})

			if shouldExist {
				if err == nil {
					return nil // bucket now exists
				}

				// If the bucket does not exist or we do not have permission to access it, the HEAD request returns one of:
				// - 400 Bad Request
				// - 403 Forbidden
				// - 404 Not Found
				// For our purposes, we will watch only for 400 & 404 since 403 likely means a fatal error that should be surfaced to the client
				var httpErr *http.ResponseError
				if errors.As(err, &httpErr) {
					if httpErr.Response != nil && (httpErr.Response.StatusCode == 400 || httpErr.Response.StatusCode == 404) {
						tflog.Debug(ctx, "got 4XX response, retrying")
						continue
					}
				}

				// any other error is fatal
				return err
			}

			// shouldExist == false: we want it gone
			if err != nil {
				var httpErr *http.ResponseError
				if errors.As(err, &httpErr) {
					if httpErr.Response != nil && (httpErr.Response.StatusCode == 400 || httpErr.Response.StatusCode == 404) {
						// bucket now deleted
						return nil
					}
				}
				// any other error (network, permission, etc.) is fatal
				return err
			}
			// still exists, keep waiting
			tflog.Debug(ctx, "I made it here somehow")
		}
	}
}

func (b *BucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data BucketResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "BUCKET DATA", map[string]interface{}{
		"zone": data.Zone.ValueString(),
		"name": data.Name.ValueString(),
	})

	s3Client, err := b.client.S3Client(ctx, data.Zone.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	// check if bucket already exists - we need to HeadBucket here because the cwobject API only errors
	// if you try to create a bucket with the same name in the same zone.
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

	_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(data.Name.ValueString()),
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(data.Zone.ValueString()),
		},
	})
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

	_, err = s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(data.Name.ValueString()),
	})
	if err != nil {
		var httpErr *http.ResponseError
		if errors.As(err, &httpErr) && httpErr.Response != nil {
			// if we get a 404 back from the client, the bucket does not exist & can be removed from state
			// we don't check 400 here because if we receive that, the bucket is in the process of being created
			if httpErr.Response.StatusCode == 404 {
				resp.State.RemoveResource(ctx)
				return
			}

			// if the bucket is still creating, return cleanly so that the resource is still reflected in Terraform state
			if httpErr.Response.StatusCode == 400 {
				return
			}
		}

		handleS3Error(err, &resp.Diagnostics, data.Name.ValueString())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update is a no-op since the only exposed fields of a bucket are immutable
func (b *BucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data BucketResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (b *BucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BucketResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "BUCKET DATA", map[string]interface{}{
		"zone": data.Zone.ValueString(),
		"name": data.Name.ValueString(),
	})

	s3Client, err := b.client.S3Client(ctx, data.Zone.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	tflog.Debug(ctx, fmt.Sprintf("CLIENT REGION: %s", s3Client.Options().Region))

	_, err = s3Client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(data.Name.ValueString()),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchBucket" {
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
	// We use 'NOTEMPTY' as the zone here because it doesn't actually matter what it is for cwobject.com
	// it only matters that it's not empty
	s3Client, err := b.client.S3Client(ctx, "NOTEMPTY")
	if err != nil {
		resp.Diagnostics.AddError("Failed to create S3 client", err.Error())
		return
	}

	bucket, err := s3Client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: func(s string) *string { return &s }(req.ID),
	})
	if err != nil {
		handleS3Error(err, &resp.Diagnostics, req.ID)
		return
	}

	data := BucketResourceModel{
		Name: types.StringValue(req.ID),
		Zone: types.StringValue(string(bucket.LocationConstraint)),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

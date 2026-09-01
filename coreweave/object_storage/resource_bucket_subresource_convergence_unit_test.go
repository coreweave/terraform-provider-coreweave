package objectstorage

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type scriptedBucketPolicyClient struct {
	putErrors   []error
	getErrors   []error
	getOutputs  []*s3.GetBucketPolicyOutput
	putInputs   []*s3.PutBucketPolicyInput
	getInputs   []*s3.GetBucketPolicyInput
	putContexts []context.Context
	getContexts []context.Context
}

func (c *scriptedBucketPolicyClient) PutBucketPolicy(
	ctx context.Context,
	input *s3.PutBucketPolicyInput,
	_ ...func(*s3.Options),
) (*s3.PutBucketPolicyOutput, error) {
	index := len(c.putInputs)
	c.putInputs = append(c.putInputs, input)
	c.putContexts = append(c.putContexts, ctx)
	if index < len(c.putErrors) && c.putErrors[index] != nil {
		return nil, c.putErrors[index]
	}
	return &s3.PutBucketPolicyOutput{}, nil
}

func (c *scriptedBucketPolicyClient) GetBucketPolicy(
	ctx context.Context,
	input *s3.GetBucketPolicyInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketPolicyOutput, error) {
	index := len(c.getInputs)
	c.getInputs = append(c.getInputs, input)
	c.getContexts = append(c.getContexts, ctx)
	if index < len(c.getErrors) && c.getErrors[index] != nil {
		return nil, c.getErrors[index]
	}
	if index < len(c.getOutputs) && c.getOutputs[index] != nil {
		return c.getOutputs[index], nil
	}
	return &s3.GetBucketPolicyOutput{}, nil
}

type scriptedBucketVersioningClient struct {
	putErrors  []error
	getErrors  []error
	getOutputs []*s3.GetBucketVersioningOutput
	putInputs  []*s3.PutBucketVersioningInput
	getInputs  []*s3.GetBucketVersioningInput
}

func (c *scriptedBucketVersioningClient) PutBucketVersioning(
	_ context.Context,
	input *s3.PutBucketVersioningInput,
	_ ...func(*s3.Options),
) (*s3.PutBucketVersioningOutput, error) {
	index := len(c.putInputs)
	c.putInputs = append(c.putInputs, input)
	if index < len(c.putErrors) && c.putErrors[index] != nil {
		return nil, c.putErrors[index]
	}
	return &s3.PutBucketVersioningOutput{}, nil
}

func (c *scriptedBucketVersioningClient) GetBucketVersioning(
	_ context.Context,
	input *s3.GetBucketVersioningInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketVersioningOutput, error) {
	index := len(c.getInputs)
	c.getInputs = append(c.getInputs, input)
	if index < len(c.getErrors) && c.getErrors[index] != nil {
		return nil, c.getErrors[index]
	}
	if index < len(c.getOutputs) && c.getOutputs[index] != nil {
		return c.getOutputs[index], nil
	}
	return &s3.GetBucketVersioningOutput{}, nil
}

func immediateSubresourcePhaseOptions(waits *int) s3PhaseOptions {
	return s3PhaseOptions{
		now:   time.Now,
		delay: func(int) time.Duration { return 0 },
		wait: func(ctx context.Context, _ time.Duration) error {
			*waits = *waits + 1
			return ctx.Err()
		},
	}
}

func TestBucketPolicyConvergenceRetriesInvalidRegion(t *testing.T) {
	t.Parallel()

	const (
		bucket = "policy-test"
		policy = `{"Version":"2012-10-17","Statement":[]}`
	)
	client := &scriptedBucketPolicyClient{
		putErrors: []error{
			&smithy.GenericAPIError{Code: errInvalidRegion},
			nil,
		},
		getErrors: []error{
			&smithy.GenericAPIError{Code: errInvalidRegion},
			nil,
		},
		getOutputs: []*s3.GetBucketPolicyOutput{
			nil,
			{Policy: aws.String(policy)},
		},
	}
	waits := 0
	options := immediateSubresourcePhaseOptions(&waits)
	ctx, cancel := bucketPropagationContext(t.Context())
	defer cancel()
	input := &s3.PutBucketPolicyInput{
		Bucket: aws.String(bucket),
		Policy: aws.String(policy),
	}

	if err := putBucketPolicy(ctx, client, bucket, input, options); err != nil {
		t.Fatalf("putBucketPolicy() error = %v", err)
	}
	if err := waitForBucketPolicy(ctx, client, bucket, policy, options); err != nil {
		t.Fatalf("waitForBucketPolicy() error = %v", err)
	}

	if got := len(client.putInputs); got != 2 {
		t.Fatalf("PutBucketPolicy calls = %d, want 2", got)
	}
	for attempt, got := range client.putInputs {
		if got != input {
			t.Fatalf("PutBucketPolicy input on attempt %d was rebuilt, want immutable input %p", attempt+1, input)
		}
	}
	if len(client.getInputs) != 2 || client.getInputs[0] != client.getInputs[1] {
		t.Fatalf("GetBucketPolicy inputs = %#v, want the same input pointer on both attempts", client.getInputs)
	}
	if waits != 2 {
		t.Fatalf("backoff waits = %d, want 2", waits)
	}
}

func TestBucketPolicyConvergenceDoesNotRetryUnrelated400(t *testing.T) {
	t.Parallel()

	client := &scriptedBucketPolicyClient{putErrors: []error{
		&smithy.GenericAPIError{Code: "InvalidBucketName"},
		nil,
	}}
	waits := 0
	input := &s3.PutBucketPolicyInput{
		Bucket: aws.String("invalid-policy-test"),
		Policy: aws.String(`{"Version":"2012-10-17","Statement":[]}`),
	}

	err := putBucketPolicy(t.Context(), client, aws.ToString(input.Bucket), input, immediateSubresourcePhaseOptions(&waits))
	if err == nil {
		t.Fatal("putBucketPolicy() error = nil, want permanent failure")
	}
	if got := len(client.putInputs); got != 1 || waits != 0 {
		t.Fatalf("PutBucketPolicy calls = %d, waits = %d, want 1 and 0", got, waits)
	}
}

func TestBucketPolicyCreateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	const (
		bucket = "policy-state-retention-test"
		policy = `{"Version":"2012-10-17","Statement":[]}`
	)
	client := &scriptedBucketPolicyClient{getErrors: []error{
		&smithy.GenericAPIError{Code: errInvalidRegion},
	}}
	resourceUnderTest := &BucketPolicyResource{
		s3ClientForConvergence: func(context.Context) (bucketPolicyAPI, error) {
			return client, nil
		},
		bucketPropagationOptions: s3PhaseOptions{
			now:   time.Now,
			delay: func(int) time.Duration { return 0 },
			wait: func(context.Context, time.Duration) error {
				return context.DeadlineExceeded
			},
		},
	}

	var schemaResponse fwresource.SchemaResponse
	resourceUnderTest.Schema(t.Context(), fwresource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("bucket policy schema diagnostics: %v", schemaResponse.Diagnostics)
	}
	model := BucketPolicyResourceModel{
		Bucket: types.StringValue(bucket),
		Policy: types.StringValue(policy),
	}
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diagnostics := plan.Set(t.Context(), &model); diagnostics.HasError() {
		t.Fatalf("set test plan: %v", diagnostics)
	}
	response := &fwresource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}

	resourceUnderTest.Create(t.Context(), fwresource.CreateRequest{Plan: plan}, response)

	if !response.Diagnostics.HasError() {
		t.Fatal("Create() diagnostics contain no error, want readback timeout")
	}
	if len(client.putInputs) != 1 || len(client.getInputs) != 1 {
		t.Fatalf("S3 calls: Put=%d Get=%d, want 1 and 1", len(client.putInputs), len(client.getInputs))
	}
	if client.putContexts[0] != client.getContexts[0] {
		t.Fatal("PutBucketPolicy and GetBucketPolicy used different propagation contexts")
	}
	var retained BucketPolicyResourceModel
	if diagnostics := response.State.Get(t.Context(), &retained); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if retained.Bucket.ValueString() != bucket || retained.Policy.ValueString() != policy {
		t.Fatalf("retained state = (%q, %q), want (%q, %q)", retained.Bucket.ValueString(), retained.Policy.ValueString(), bucket, policy)
	}
}

func TestBucketVersioningConvergenceRetriesInvalidRegion(t *testing.T) {
	t.Parallel()

	const bucket = "versioning-test"
	const status = s3types.BucketVersioningStatusEnabled
	client := &scriptedBucketVersioningClient{
		putErrors: []error{
			&smithy.GenericAPIError{Code: errInvalidRegion},
			nil,
		},
		getErrors: []error{
			&smithy.GenericAPIError{Code: errInvalidRegion},
			nil,
		},
		getOutputs: []*s3.GetBucketVersioningOutput{
			nil,
			{Status: status},
		},
	}
	waits := 0
	options := immediateSubresourcePhaseOptions(&waits)
	ctx, cancel := bucketPropagationContext(t.Context())
	defer cancel()
	input := &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: status,
		},
	}

	if err := putBucketVersioning(ctx, client, bucket, input, options); err != nil {
		t.Fatalf("putBucketVersioning() error = %v", err)
	}
	if err := waitForBucketVersioning(ctx, client, bucket, status, options); err != nil {
		t.Fatalf("waitForBucketVersioning() error = %v", err)
	}

	if got := len(client.putInputs); got != 2 {
		t.Fatalf("PutBucketVersioning calls = %d, want 2", got)
	}
	for attempt, got := range client.putInputs {
		if got != input {
			t.Fatalf("PutBucketVersioning input on attempt %d was rebuilt, want immutable input %p", attempt+1, input)
		}
	}
	if len(client.getInputs) != 2 || client.getInputs[0] != client.getInputs[1] {
		t.Fatalf("GetBucketVersioning inputs = %#v, want the same input pointer on both attempts", client.getInputs)
	}
	if waits != 2 {
		t.Fatalf("backoff waits = %d, want 2", waits)
	}
}

func TestBucketVersioningConvergenceDoesNotRetryUnrelated400(t *testing.T) {
	t.Parallel()

	client := &scriptedBucketVersioningClient{getErrors: []error{
		&smithy.GenericAPIError{Code: "InvalidRequest"},
		nil,
	}}
	waits := 0

	err := waitForBucketVersioning(
		t.Context(),
		client,
		"invalid-versioning-test",
		s3types.BucketVersioningStatusEnabled,
		immediateSubresourcePhaseOptions(&waits),
	)
	if err == nil {
		t.Fatal("waitForBucketVersioning() error = nil, want permanent failure")
	}
	if len(client.getInputs) != 1 || waits != 0 {
		t.Fatalf("GetBucketVersioning calls = %d, waits = %d, want 1 and 0", len(client.getInputs), waits)
	}
}

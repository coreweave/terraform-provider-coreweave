package objectstorage

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
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
	putErrors   []error
	getErrors   []error
	getOutputs  []*s3.GetBucketVersioningOutput
	putInputs   []*s3.PutBucketVersioningInput
	getInputs   []*s3.GetBucketVersioningInput
	putContexts []context.Context
	getContexts []context.Context
}

func (c *scriptedBucketVersioningClient) PutBucketVersioning(
	ctx context.Context,
	input *s3.PutBucketVersioningInput,
	_ ...func(*s3.Options),
) (*s3.PutBucketVersioningOutput, error) {
	index := len(c.putInputs)
	c.putInputs = append(c.putInputs, input)
	c.putContexts = append(c.putContexts, ctx)
	if index < len(c.putErrors) && c.putErrors[index] != nil {
		return nil, c.putErrors[index]
	}
	return &s3.PutBucketVersioningOutput{}, nil
}

func (c *scriptedBucketVersioningClient) GetBucketVersioning(
	ctx context.Context,
	input *s3.GetBucketVersioningInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketVersioningOutput, error) {
	index := len(c.getInputs)
	c.getInputs = append(c.getInputs, input)
	c.getContexts = append(c.getContexts, ctx)
	if index < len(c.getErrors) && c.getErrors[index] != nil {
		return nil, c.getErrors[index]
	}
	if index < len(c.getOutputs) && c.getOutputs[index] != nil {
		return c.getOutputs[index], nil
	}
	return &s3.GetBucketVersioningOutput{}, nil
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
			{Policy: aws.String(policy)},
		},
	}
	waits := 0
	options := immediateS3PhaseOptions(&waits)
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
	if len(client.getInputs) != 3 || client.getInputs[0] != client.getInputs[1] || client.getInputs[1] != client.getInputs[2] {
		t.Fatalf("GetBucketPolicy inputs = %#v, want the same input pointer on all attempts", client.getInputs)
	}
	if waits != 3 {
		t.Fatalf("backoff waits = %d, want 3", waits)
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

	err := putBucketPolicy(t.Context(), client, aws.ToString(input.Bucket), input, immediateS3PhaseOptions(&waits))
	if err == nil {
		t.Fatal("putBucketPolicy() error = nil, want permanent failure")
	}
	if got := len(client.putInputs); got != 1 || waits != 0 {
		t.Fatalf("PutBucketPolicy calls = %d, waits = %d, want 1 and 0", got, waits)
	}
}

func TestWaitForBucketPolicyRetriesNilResponse(t *testing.T) {
	t.Parallel()

	const policy = `{"Version":"2012-10-17","Statement":[]}`
	client := &scriptedBucketPolicyClient{getOutputs: []*s3.GetBucketPolicyOutput{
		{},
		{Policy: aws.String(policy)},
		{Policy: aws.String(policy)},
	}}
	waits := 0

	if err := waitForBucketPolicy(t.Context(), client, "policy-test", policy, immediateS3PhaseOptions(&waits)); err != nil {
		t.Fatalf("waitForBucketPolicy() error = %v", err)
	}
	if len(client.getInputs) != 3 || waits != 2 {
		t.Fatalf("GetBucketPolicy calls = %d, waits = %d; want 3, 2", len(client.getInputs), waits)
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
	}, putErrors: []error{
		&smithy.GenericAPIError{Code: errInvalidRegion},
		nil,
	}}
	resourceUnderTest := &BucketPolicyResource{
		s3ClientForConvergence: func(context.Context) (bucketPolicyAPI, error) {
			return client, nil
		},
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnWait(2),
	}

	model := BucketPolicyResourceModel{
		Bucket: types.StringValue(bucket),
		Policy: types.StringValue(policy),
	}
	response := runCreateWithModel(t, resourceUnderTest, model)
	assertWriteRetriedAndRetainedState(t, response.Diagnostics.HasError(), len(client.putInputs), len(client.getInputs), client.putContexts, client.getContexts)
	if client.putInputs[0] != client.putInputs[1] {
		t.Fatal("PutBucketPolicy rebuilt its retry input")
	}
	var retained BucketPolicyResourceModel
	if diagnostics := response.State.Get(t.Context(), &retained); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if retained.Bucket.ValueString() != bucket || retained.Policy.ValueString() != policy {
		t.Fatalf("retained state = (%q, %q), want (%q, %q)", retained.Bucket.ValueString(), retained.Policy.ValueString(), bucket, policy)
	}
}

func s3PhaseOptionsTimingOutOnWait(timeoutOn int) s3PhaseOptions {
	waits := 0
	return s3PhaseOptions{
		now:   time.Now,
		delay: func(int) time.Duration { return 0 },
		wait: func(context.Context, time.Duration) error {
			waits++
			if waits >= timeoutOn {
				return context.DeadlineExceeded
			}
			return nil
		},
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
			{Status: status},
		},
	}
	waits := 0
	options := immediateS3PhaseOptions(&waits)
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
	if len(client.getInputs) != 3 || client.getInputs[0] != client.getInputs[1] || client.getInputs[1] != client.getInputs[2] {
		t.Fatalf("GetBucketVersioning inputs = %#v, want the same input pointer on all attempts", client.getInputs)
	}
	if waits != 3 {
		t.Fatalf("backoff waits = %d, want 3", waits)
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
		immediateS3PhaseOptions(&waits),
	)
	if err == nil {
		t.Fatal("waitForBucketVersioning() error = nil, want permanent failure")
	}
	if len(client.getInputs) != 1 || waits != 0 {
		t.Fatalf("GetBucketVersioning calls = %d, waits = %d, want 1 and 0", len(client.getInputs), waits)
	}
}

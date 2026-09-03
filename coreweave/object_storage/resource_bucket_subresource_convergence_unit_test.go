package objectstorage

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type scriptedBucketPolicyClient struct {
	put scriptedS3Call[s3.PutBucketPolicyInput, s3.PutBucketPolicyOutput]
	get scriptedS3Call[s3.GetBucketPolicyInput, s3.GetBucketPolicyOutput]
}

func (c *scriptedBucketPolicyClient) PutBucketPolicy(
	ctx context.Context,
	input *s3.PutBucketPolicyInput,
	_ ...func(*s3.Options),
) (*s3.PutBucketPolicyOutput, error) {
	return c.put.call(ctx, input)
}

func (c *scriptedBucketPolicyClient) GetBucketPolicy(
	ctx context.Context,
	input *s3.GetBucketPolicyInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketPolicyOutput, error) {
	return c.get.call(ctx, input)
}

type scriptedBucketVersioningClient struct {
	put scriptedS3Call[s3.PutBucketVersioningInput, s3.PutBucketVersioningOutput]
	get scriptedS3Call[s3.GetBucketVersioningInput, s3.GetBucketVersioningOutput]
}

func (c *scriptedBucketVersioningClient) PutBucketVersioning(
	ctx context.Context,
	input *s3.PutBucketVersioningInput,
	_ ...func(*s3.Options),
) (*s3.PutBucketVersioningOutput, error) {
	return c.put.call(ctx, input)
}

func (c *scriptedBucketVersioningClient) GetBucketVersioning(
	ctx context.Context,
	input *s3.GetBucketVersioningInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketVersioningOutput, error) {
	return c.get.call(ctx, input)
}

func TestBucketPolicyConvergenceRetriesInvalidRegion(t *testing.T) {
	t.Parallel()

	const (
		bucket = "policy-test"
		policy = `{"Version":"2012-10-17","Statement":[]}`
	)
	client := &scriptedBucketPolicyClient{}
	client.put.errors = []error{
		&smithy.GenericAPIError{Code: errInvalidRegion},
		nil,
	}
	client.get.errors = []error{
		&smithy.GenericAPIError{Code: errInvalidRegion},
		nil,
	}
	client.get.outputs = []*s3.GetBucketPolicyOutput{
		nil,
		{Policy: aws.String(policy)},
		{Policy: aws.String(policy)},
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

	if got := len(client.put.inputs); got != 2 {
		t.Fatalf("PutBucketPolicy calls = %d, want 2", got)
	}
	for attempt, got := range client.put.inputs {
		if got != input {
			t.Fatalf("PutBucketPolicy input on attempt %d was rebuilt, want immutable input %p", attempt+1, input)
		}
	}
	if len(client.get.inputs) != 3 || client.get.inputs[0] != client.get.inputs[1] || client.get.inputs[1] != client.get.inputs[2] {
		t.Fatalf("GetBucketPolicy inputs = %#v, want the same input pointer on all attempts", client.get.inputs)
	}
	if waits != 3 {
		t.Fatalf("backoff waits = %d, want 3", waits)
	}
}

func TestWaitForBucketPolicyRetriesNilResponse(t *testing.T) {
	t.Parallel()

	const policy = `{"Version":"2012-10-17","Statement":[]}`
	client := &scriptedBucketPolicyClient{}
	client.get.outputs = []*s3.GetBucketPolicyOutput{
		{},
		{Policy: aws.String(policy)},
		{Policy: aws.String(policy)},
	}
	waits := 0

	if err := waitForBucketPolicy(t.Context(), client, "policy-test", policy, immediateS3PhaseOptions(&waits)); err != nil {
		t.Fatalf("waitForBucketPolicy() error = %v", err)
	}
	if len(client.get.inputs) != 3 || waits != 2 {
		t.Fatalf("GetBucketPolicy calls = %d, waits = %d; want 3, 2", len(client.get.inputs), waits)
	}
}

func TestBucketVersioningConvergenceRetriesInvalidRegion(t *testing.T) {
	t.Parallel()

	const bucket = "versioning-test"
	const status = s3types.BucketVersioningStatusEnabled
	client := &scriptedBucketVersioningClient{}
	client.put.errors = []error{
		&smithy.GenericAPIError{Code: errInvalidRegion},
		nil,
	}
	client.get.errors = []error{
		&smithy.GenericAPIError{Code: errInvalidRegion},
		nil,
	}
	client.get.outputs = []*s3.GetBucketVersioningOutput{
		nil,
		{Status: status},
		{Status: status},
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

	if got := len(client.put.inputs); got != 2 {
		t.Fatalf("PutBucketVersioning calls = %d, want 2", got)
	}
	for attempt, got := range client.put.inputs {
		if got != input {
			t.Fatalf("PutBucketVersioning input on attempt %d was rebuilt, want immutable input %p", attempt+1, input)
		}
	}
	if len(client.get.inputs) != 3 || client.get.inputs[0] != client.get.inputs[1] || client.get.inputs[1] != client.get.inputs[2] {
		t.Fatalf("GetBucketVersioning inputs = %#v, want the same input pointer on all attempts", client.get.inputs)
	}
	if waits != 3 {
		t.Fatalf("backoff waits = %d, want 3", waits)
	}
}

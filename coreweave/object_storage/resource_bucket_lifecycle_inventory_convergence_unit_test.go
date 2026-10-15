package objectstorage

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type scriptedLifecycleConfigurationClient struct {
	put scriptedS3Call[s3.PutBucketLifecycleConfigurationInput, s3.PutBucketLifecycleConfigurationOutput]
	get scriptedS3Call[s3.GetBucketLifecycleConfigurationInput, s3.GetBucketLifecycleConfigurationOutput]
}

func (c *scriptedLifecycleConfigurationClient) PutBucketLifecycleConfiguration(
	ctx context.Context,
	input *s3.PutBucketLifecycleConfigurationInput,
	_ ...func(*s3.Options),
) (*s3.PutBucketLifecycleConfigurationOutput, error) {
	return c.put.call(ctx, input)
}

func (c *scriptedLifecycleConfigurationClient) GetBucketLifecycleConfiguration(
	ctx context.Context,
	input *s3.GetBucketLifecycleConfigurationInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	return c.get.call(ctx, input)
}

type scriptedInventoryConfigurationClient struct {
	put scriptedS3Call[s3.PutBucketInventoryConfigurationInput, s3.PutBucketInventoryConfigurationOutput]
	get scriptedS3Call[s3.GetBucketInventoryConfigurationInput, s3.GetBucketInventoryConfigurationOutput]
}

func (c *scriptedInventoryConfigurationClient) PutBucketInventoryConfiguration(
	ctx context.Context,
	input *s3.PutBucketInventoryConfigurationInput,
	_ ...func(*s3.Options),
) (*s3.PutBucketInventoryConfigurationOutput, error) {
	return c.put.call(ctx, input)
}

func (c *scriptedInventoryConfigurationClient) GetBucketInventoryConfiguration(
	ctx context.Context,
	input *s3.GetBucketInventoryConfigurationInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketInventoryConfigurationOutput, error) {
	return c.get.call(ctx, input)
}

func TestBucketLifecycleConfigConvergenceRetriesInvalidRegion(t *testing.T) {
	t.Parallel()

	expected := s3types.BucketLifecycleConfiguration{Rules: []s3types.LifecycleRule{{
		ID:     aws.String("expire-logs"),
		Status: s3types.ExpirationStatusEnabled,
	}}}
	putInput := &s3.PutBucketLifecycleConfigurationInput{
		Bucket:                 aws.String("source-bucket"),
		LifecycleConfiguration: &expected,
	}
	client := &scriptedLifecycleConfigurationClient{}
	client.put.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.outputs = []*s3.GetBucketLifecycleConfigurationOutput{
		nil,
		{Rules: expected.Rules},
		{Rules: expected.Rules},
	}
	waits := 0
	options := immediateS3PhaseOptions(&waits)

	if err := putBucketLifecycleConfig(t.Context(), client, "source-bucket", putInput, options); err != nil {
		t.Fatalf("putBucketLifecycleConfig() error = %v", err)
	}
	result, err := waitForLifecycleConfig(t.Context(), client, "source-bucket", expected, options)
	if err != nil {
		t.Fatalf("waitForLifecycleConfig() error = %v", err)
	}
	if result == nil || len(result.Rules) != 1 {
		t.Fatalf("waitForLifecycleConfig() result = %#v, want expected rule", result)
	}
	if len(client.put.inputs) != 2 || client.put.inputs[0] != putInput || client.put.inputs[1] != putInput {
		t.Fatalf("Put inputs = %#v, want the same input pointer on both attempts", client.put.inputs)
	}
	if len(client.get.inputs) != 3 || client.get.inputs[0] != client.get.inputs[1] || client.get.inputs[1] != client.get.inputs[2] {
		t.Fatalf("Get inputs = %#v, want the same input pointer on all attempts", client.get.inputs)
	}
	if waits != 3 {
		t.Fatalf("backoff waits = %d, want 3", waits)
	}
}

func TestBucketInventoryConfigConvergenceRetriesInvalidRegion(t *testing.T) {
	t.Parallel()

	expected := s3types.InventoryConfiguration{
		Id:                     aws.String("daily"),
		IsEnabled:              aws.Bool(true),
		IncludedObjectVersions: s3types.InventoryIncludedObjectVersionsAll,
	}
	putInput := &s3.PutBucketInventoryConfigurationInput{
		Bucket:                 aws.String("source-bucket"),
		Id:                     expected.Id,
		InventoryConfiguration: &expected,
	}
	client := &scriptedInventoryConfigurationClient{}
	client.put.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.outputs = []*s3.GetBucketInventoryConfigurationOutput{
		nil,
		{InventoryConfiguration: &expected},
		{InventoryConfiguration: &expected},
	}
	waits := 0
	options := immediateS3PhaseOptions(&waits)

	if err := putBucketInventoryConfig(t.Context(), client, "source-bucket", putInput, options); err != nil {
		t.Fatalf("putBucketInventoryConfig() error = %v", err)
	}
	result, err := waitForInventoryConfig(t.Context(), client, "source-bucket", "daily", expected, options)
	if err != nil {
		t.Fatalf("waitForInventoryConfig() error = %v", err)
	}
	if result == nil || result.InventoryConfiguration == nil {
		t.Fatalf("waitForInventoryConfig() result = %#v, want expected configuration", result)
	}
	if len(client.put.inputs) != 2 || client.put.inputs[0] != putInput || client.put.inputs[1] != putInput {
		t.Fatalf("Put inputs = %#v, want the same input pointer on both attempts", client.put.inputs)
	}
	if len(client.get.inputs) != 3 || client.get.inputs[0] != client.get.inputs[1] || client.get.inputs[1] != client.get.inputs[2] {
		t.Fatalf("Get inputs = %#v, want the same input pointer on all attempts", client.get.inputs)
	}
	if waits != 3 {
		t.Fatalf("backoff waits = %d, want 3", waits)
	}
}

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
	putErrors  []error
	putInputs  []*s3.PutBucketLifecycleConfigurationInput
	getErrors  []error
	getOutputs []*s3.GetBucketLifecycleConfigurationOutput
	getInputs  []*s3.GetBucketLifecycleConfigurationInput
}

func (c *scriptedLifecycleConfigurationClient) PutBucketLifecycleConfiguration(
	_ context.Context,
	input *s3.PutBucketLifecycleConfigurationInput,
	_ ...func(*s3.Options),
) (*s3.PutBucketLifecycleConfigurationOutput, error) {
	c.putInputs = append(c.putInputs, input)
	index := len(c.putInputs) - 1
	if index < len(c.putErrors) && c.putErrors[index] != nil {
		return nil, c.putErrors[index]
	}
	return &s3.PutBucketLifecycleConfigurationOutput{}, nil
}

func (c *scriptedLifecycleConfigurationClient) GetBucketLifecycleConfiguration(
	_ context.Context,
	input *s3.GetBucketLifecycleConfigurationInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	c.getInputs = append(c.getInputs, input)
	index := len(c.getInputs) - 1
	if index < len(c.getErrors) && c.getErrors[index] != nil {
		return nil, c.getErrors[index]
	}
	if index < len(c.getOutputs) {
		return c.getOutputs[index], nil
	}
	return &s3.GetBucketLifecycleConfigurationOutput{}, nil
}

type scriptedInventoryConfigurationClient struct {
	putErrors  []error
	putInputs  []*s3.PutBucketInventoryConfigurationInput
	getErrors  []error
	getOutputs []*s3.GetBucketInventoryConfigurationOutput
	getInputs  []*s3.GetBucketInventoryConfigurationInput
}

func (c *scriptedInventoryConfigurationClient) PutBucketInventoryConfiguration(
	_ context.Context,
	input *s3.PutBucketInventoryConfigurationInput,
	_ ...func(*s3.Options),
) (*s3.PutBucketInventoryConfigurationOutput, error) {
	c.putInputs = append(c.putInputs, input)
	index := len(c.putInputs) - 1
	if index < len(c.putErrors) && c.putErrors[index] != nil {
		return nil, c.putErrors[index]
	}
	return &s3.PutBucketInventoryConfigurationOutput{}, nil
}

func (c *scriptedInventoryConfigurationClient) GetBucketInventoryConfiguration(
	_ context.Context,
	input *s3.GetBucketInventoryConfigurationInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketInventoryConfigurationOutput, error) {
	c.getInputs = append(c.getInputs, input)
	index := len(c.getInputs) - 1
	if index < len(c.getErrors) && c.getErrors[index] != nil {
		return nil, c.getErrors[index]
	}
	if index < len(c.getOutputs) {
		return c.getOutputs[index], nil
	}
	return &s3.GetBucketInventoryConfigurationOutput{}, nil
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
	client := &scriptedLifecycleConfigurationClient{
		putErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getOutputs: []*s3.GetBucketLifecycleConfigurationOutput{
			nil,
			{Rules: expected.Rules},
		},
	}
	waits := 0
	options := immediatePhaseOptions(&waits)

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
	if len(client.putInputs) != 2 || client.putInputs[0] != putInput || client.putInputs[1] != putInput {
		t.Fatalf("Put inputs = %#v, want the same input pointer on both attempts", client.putInputs)
	}
	if len(client.getInputs) != 2 || client.getInputs[0] != client.getInputs[1] {
		t.Fatalf("Get inputs = %#v, want the same input pointer on both attempts", client.getInputs)
	}
	if waits != 2 {
		t.Fatalf("backoff waits = %d, want 2", waits)
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
	client := &scriptedInventoryConfigurationClient{
		putErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getOutputs: []*s3.GetBucketInventoryConfigurationOutput{
			nil,
			{InventoryConfiguration: &expected},
		},
	}
	waits := 0
	options := immediatePhaseOptions(&waits)

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
	if len(client.putInputs) != 2 || client.putInputs[0] != putInput || client.putInputs[1] != putInput {
		t.Fatalf("Put inputs = %#v, want the same input pointer on both attempts", client.putInputs)
	}
	if len(client.getInputs) != 2 || client.getInputs[0] != client.getInputs[1] {
		t.Fatalf("Get inputs = %#v, want the same input pointer on both attempts", client.getInputs)
	}
	if waits != 2 {
		t.Fatalf("backoff waits = %d, want 2", waits)
	}
}

func TestBucketConfigurationConvergenceDoesNotRetryPermanentErrors(t *testing.T) {
	t.Parallel()

	t.Run("lifecycle write", func(t *testing.T) {
		t.Parallel()
		client := &scriptedLifecycleConfigurationClient{putErrors: []error{
			&smithy.GenericAPIError{Code: "InvalidBucketName"},
			nil,
		}}
		waits := 0
		err := putBucketLifecycleConfig(
			t.Context(),
			client,
			"invalid",
			&s3.PutBucketLifecycleConfigurationInput{Bucket: aws.String("invalid")},
			immediatePhaseOptions(&waits),
		)
		if err == nil {
			t.Fatal("putBucketLifecycleConfig() error = nil, want permanent failure")
		}
		if len(client.putInputs) != 1 || waits != 0 {
			t.Fatalf("Put calls = %d, waits = %d, want 1 and 0", len(client.putInputs), waits)
		}
	})

	t.Run("inventory readback", func(t *testing.T) {
		t.Parallel()
		client := &scriptedInventoryConfigurationClient{getErrors: []error{
			&smithy.GenericAPIError{Code: "AccessDenied"},
			nil,
		}}
		waits := 0
		_, err := waitForInventoryConfig(
			t.Context(),
			client,
			"source-bucket",
			"daily",
			s3types.InventoryConfiguration{},
			immediatePhaseOptions(&waits),
		)
		if err == nil {
			t.Fatal("waitForInventoryConfig() error = nil, want permanent failure")
		}
		if len(client.getInputs) != 1 || waits != 0 {
			t.Fatalf("Get calls = %d, waits = %d, want 1 and 0", len(client.getInputs), waits)
		}
	})
}

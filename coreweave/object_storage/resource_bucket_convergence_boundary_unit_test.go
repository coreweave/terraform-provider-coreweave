package objectstorage

import (
	"context"
	"testing"

	"github.com/aws/smithy-go"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketPolicyCreateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedBucketPolicyClient{}
	client.put.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}}
	resourceUnderTest := &BucketPolicyResource{
		s3ClientForConvergence:   func(context.Context) (bucketPolicyAPI, error) { return client, nil },
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := BucketPolicyResourceModel{
		Bucket: types.StringValue("policy-state-retention-test"),
		Policy: types.StringValue(`{"Version":"2012-10-17","Statement":[]}`),
	}

	response := runCreateWithModel(t, resourceUnderTest, model)
	assertReadbackTimeoutRetainsPlan(t, response.State, response.Diagnostics.HasError(), model, &client.put, &client.get)
}

func TestBucketVersioningCreateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedBucketVersioningClient{}
	client.put.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}}
	resourceUnderTest := &BucketVersioningResource{
		s3ClientForConvergence:   func(context.Context) (bucketVersioningAPI, error) { return client, nil },
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := BucketVersioningResourceModel{
		Bucket: types.StringValue("versioning-state-retention-test"),
		VersioningConfiguration: VersioningConfigurationModel{
			Status: types.StringValue("Enabled"),
		},
	}

	response := runCreateWithModel(t, resourceUnderTest, model)
	assertReadbackTimeoutRetainsPlan(t, response.State, response.Diagnostics.HasError(), model, &client.put, &client.get)
}

func TestBucketPolicyUpdateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedBucketPolicyClient{}
	client.put.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}}
	resourceUnderTest := &BucketPolicyResource{
		s3ClientForConvergence:   func(context.Context) (bucketPolicyAPI, error) { return client, nil },
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := BucketPolicyResourceModel{
		Bucket: types.StringValue("policy-update-state-retention-test"),
		Policy: types.StringValue(`{"Version":"2012-10-17","Statement":[]}`),
	}

	response := runUpdateWithModel(t, resourceUnderTest, model)
	assertReadbackTimeoutRetainsPlan(t, response.State, response.Diagnostics.HasError(), model, &client.put, &client.get)
}

func TestBucketVersioningUpdateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedBucketVersioningClient{}
	client.put.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}}
	resourceUnderTest := &BucketVersioningResource{
		s3ClientForConvergence:   func(context.Context) (bucketVersioningAPI, error) { return client, nil },
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := BucketVersioningResourceModel{
		Bucket: types.StringValue("versioning-update-state-retention-test"),
		VersioningConfiguration: VersioningConfigurationModel{
			Status: types.StringValue("Suspended"),
		},
	}

	response := runUpdateWithModel(t, resourceUnderTest, model)
	assertReadbackTimeoutRetainsPlan(t, response.State, response.Diagnostics.HasError(), model, &client.put, &client.get)
}

func TestBucketLifecycleCreateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedLifecycleConfigurationClient{}
	client.put.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}}
	resourceUnderTest := &BucketLifecycleResource{
		s3ClientForConvergence:   func(context.Context) (bucketLifecycleConfigurationClient, error) { return client, nil },
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := BucketLifecycleResourceModel{
		Bucket: types.StringValue("lifecycle-state-retention-test"),
		Rule: []LifecycleRuleModel{{
			ID:     types.StringValue("expire-logs"),
			Prefix: types.StringNull(),
			Status: types.StringValue("Enabled"),
			Expiration: &ExpirationModel{
				Date:                      types.StringNull(),
				Days:                      types.Int32Value(30),
				ExpiredObjectDeleteMarker: types.BoolNull(),
			},
		}},
	}

	response := runCreateWithModel(t, resourceUnderTest, model)
	assertReadbackTimeoutRetainsPlan(t, response.State, response.Diagnostics.HasError(), model, &client.put, &client.get)
}

func TestBucketLifecycleUpdateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedLifecycleConfigurationClient{}
	client.put.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}}
	resourceUnderTest := &BucketLifecycleResource{
		s3ClientForConvergence:   func(context.Context) (bucketLifecycleConfigurationClient, error) { return client, nil },
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := BucketLifecycleResourceModel{
		Bucket: types.StringValue("lifecycle-update-state-retention-test"),
		Rule: []LifecycleRuleModel{{
			ID:     types.StringValue("expire-updated-logs"),
			Prefix: types.StringNull(),
			Status: types.StringValue("Enabled"),
			Expiration: &ExpirationModel{
				Date:                      types.StringNull(),
				Days:                      types.Int32Value(60),
				ExpiredObjectDeleteMarker: types.BoolNull(),
			},
		}},
	}

	response := runUpdateWithModel(t, resourceUnderTest, model)
	assertReadbackTimeoutRetainsPlan(t, response.State, response.Diagnostics.HasError(), model, &client.put, &client.get)
}

func TestBucketInventoryCreateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedInventoryConfigurationClient{}
	client.put.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}}
	resourceUnderTest := &BucketInventoryResource{
		s3ClientForConvergence:   func(context.Context) (bucketInventoryConfigurationClient, error) { return client, nil },
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := *fullInventoryModel()

	response := runCreateWithModel(t, resourceUnderTest, model)
	assertReadbackTimeoutRetainsPlan(t, response.State, response.Diagnostics.HasError(), model, &client.put, &client.get)
}

func TestBucketInventoryUpdateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedInventoryConfigurationClient{}
	client.put.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil}
	client.get.errors = []error{&smithy.GenericAPIError{Code: errInvalidRegion}}
	resourceUnderTest := &BucketInventoryResource{
		s3ClientForConvergence:   func(context.Context) (bucketInventoryConfigurationClient, error) { return client, nil },
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := *fullInventoryModel()
	model.Enabled = types.BoolValue(false)

	response := runUpdateWithModel(t, resourceUnderTest, model)
	assertReadbackTimeoutRetainsPlan(t, response.State, response.Diagnostics.HasError(), model, &client.put, &client.get)
}

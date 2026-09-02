package objectstorage

import (
	"context"
	"reflect"
	"testing"

	"github.com/aws/smithy-go"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketVersioningCreateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedBucketVersioningClient{
		putErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}},
	}
	resourceUnderTest := &BucketVersioningResource{
		s3ClientForConvergence: func(context.Context) (bucketVersioningAPI, error) {
			return client, nil
		},
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := BucketVersioningResourceModel{
		Bucket: types.StringValue("versioning-state-retention-test"),
		VersioningConfiguration: VersioningConfigurationModel{
			Status: types.StringValue("Enabled"),
		},
	}

	response := runCreateWithModel(t, resourceUnderTest, model)
	assertWriteRetriedAndRetainedState(t, response.Diagnostics.HasError(), len(client.putInputs), len(client.getInputs), client.putContexts, client.getContexts)
	if client.putInputs[0] != client.putInputs[1] {
		t.Fatal("PutBucketVersioning rebuilt its retry input")
	}

	var retained BucketVersioningResourceModel
	if diagnostics := response.State.Get(t.Context(), &retained); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if !reflect.DeepEqual(retained, model) {
		t.Fatalf("retained state = %#v, want %#v", retained, model)
	}
}

func TestBucketPolicyUpdateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	const policy = `{"Version":"2012-10-17","Statement":[]}`
	client := &scriptedBucketPolicyClient{
		putErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}},
	}
	resourceUnderTest := &BucketPolicyResource{
		s3ClientForConvergence: func(context.Context) (bucketPolicyAPI, error) {
			return client, nil
		},
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := BucketPolicyResourceModel{
		Bucket: types.StringValue("policy-update-state-retention-test"),
		Policy: types.StringValue(policy),
	}

	response := runUpdateWithModel(t, resourceUnderTest, model)
	assertWriteRetriedAndRetainedState(t, response.Diagnostics.HasError(), len(client.putInputs), len(client.getInputs), client.putContexts, client.getContexts)
	if client.putInputs[0] != client.putInputs[1] {
		t.Fatal("PutBucketPolicy rebuilt its retry input")
	}

	var retained BucketPolicyResourceModel
	if diagnostics := response.State.Get(t.Context(), &retained); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if !reflect.DeepEqual(retained, model) {
		t.Fatalf("retained state = %#v, want %#v", retained, model)
	}
}

func TestBucketVersioningUpdateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedBucketVersioningClient{
		putErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}},
	}
	resourceUnderTest := &BucketVersioningResource{
		s3ClientForConvergence: func(context.Context) (bucketVersioningAPI, error) {
			return client, nil
		},
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := BucketVersioningResourceModel{
		Bucket: types.StringValue("versioning-update-state-retention-test"),
		VersioningConfiguration: VersioningConfigurationModel{
			Status: types.StringValue("Suspended"),
		},
	}

	response := runUpdateWithModel(t, resourceUnderTest, model)
	assertWriteRetriedAndRetainedState(t, response.Diagnostics.HasError(), len(client.putInputs), len(client.getInputs), client.putContexts, client.getContexts)
	if client.putInputs[0] != client.putInputs[1] {
		t.Fatal("PutBucketVersioning rebuilt its retry input")
	}

	var retained BucketVersioningResourceModel
	if diagnostics := response.State.Get(t.Context(), &retained); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if !reflect.DeepEqual(retained, model) {
		t.Fatalf("retained state = %#v, want %#v", retained, model)
	}
}

func TestBucketLifecycleCreateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedLifecycleConfigurationClient{
		putErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}},
	}
	resourceUnderTest := &BucketLifecycleResource{
		s3ClientForConvergence: func(context.Context) (bucketLifecycleConfigurationClient, error) {
			return client, nil
		},
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
	assertWriteRetriedAndRetainedState(t, response.Diagnostics.HasError(), len(client.putInputs), len(client.getInputs), client.putContexts, client.getContexts)
	if client.putInputs[0] != client.putInputs[1] {
		t.Fatal("PutBucketLifecycleConfiguration rebuilt its retry input")
	}

	var retained BucketLifecycleResourceModel
	if diagnostics := response.State.Get(t.Context(), &retained); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if !reflect.DeepEqual(retained, model) {
		t.Fatalf("retained state = %#v, want lifecycle plan", retained)
	}
}

func TestBucketLifecycleUpdateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedLifecycleConfigurationClient{
		putErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}},
	}
	resourceUnderTest := &BucketLifecycleResource{
		s3ClientForConvergence: func(context.Context) (bucketLifecycleConfigurationClient, error) {
			return client, nil
		},
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
	assertWriteRetriedAndRetainedState(t, response.Diagnostics.HasError(), len(client.putInputs), len(client.getInputs), client.putContexts, client.getContexts)
	if client.putInputs[0] != client.putInputs[1] {
		t.Fatal("PutBucketLifecycleConfiguration rebuilt its retry input")
	}

	var retained BucketLifecycleResourceModel
	if diagnostics := response.State.Get(t.Context(), &retained); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if !reflect.DeepEqual(retained, model) {
		t.Fatalf("retained state = %#v, want %#v", retained, model)
	}
}

func TestBucketInventoryCreateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedInventoryConfigurationClient{
		putErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}},
	}
	resourceUnderTest := &BucketInventoryResource{
		s3ClientForConvergence: func(context.Context) (bucketInventoryConfigurationClient, error) {
			return client, nil
		},
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := *fullInventoryModel()

	response := runCreateWithModel(t, resourceUnderTest, model)
	assertWriteRetriedAndRetainedState(t, response.Diagnostics.HasError(), len(client.putInputs), len(client.getInputs), client.putContexts, client.getContexts)
	if client.putInputs[0] != client.putInputs[1] {
		t.Fatal("PutBucketInventoryConfiguration rebuilt its retry input")
	}

	var retained BucketInventoryResourceModel
	if diagnostics := response.State.Get(t.Context(), &retained); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if !reflect.DeepEqual(retained, model) {
		t.Fatalf("retained state = %#v, want inventory plan", retained)
	}
}

func TestBucketInventoryUpdateRetainsStateWhenReadbackTimesOut(t *testing.T) {
	t.Parallel()

	client := &scriptedInventoryConfigurationClient{
		putErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
		getErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}},
	}
	resourceUnderTest := &BucketInventoryResource{
		s3ClientForConvergence: func(context.Context) (bucketInventoryConfigurationClient, error) {
			return client, nil
		},
		bucketPropagationOptions: s3PhaseOptionsTimingOutOnSecondWait(),
	}
	model := *fullInventoryModel()
	model.Enabled = types.BoolValue(false)

	response := runUpdateWithModel(t, resourceUnderTest, model)
	assertWriteRetriedAndRetainedState(t, response.Diagnostics.HasError(), len(client.putInputs), len(client.getInputs), client.putContexts, client.getContexts)
	if client.putInputs[0] != client.putInputs[1] {
		t.Fatal("PutBucketInventoryConfiguration rebuilt its retry input")
	}

	var retained BucketInventoryResourceModel
	if diagnostics := response.State.Get(t.Context(), &retained); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if !reflect.DeepEqual(retained, model) {
		t.Fatalf("retained state = %#v, want %#v", retained, model)
	}
}

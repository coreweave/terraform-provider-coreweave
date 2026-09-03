package objectstorage

import (
	"context"
	"reflect"
	"testing"
	"time"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

type scriptedS3Call[Input, Output any] struct {
	errors   []error
	outputs  []*Output
	inputs   []*Input
	contexts []context.Context
}

func (c *scriptedS3Call[Input, Output]) call(ctx context.Context, input *Input) (*Output, error) {
	index := len(c.inputs)
	c.inputs = append(c.inputs, input)
	c.contexts = append(c.contexts, ctx)
	if index < len(c.errors) && c.errors[index] != nil {
		return nil, c.errors[index]
	}
	if index < len(c.outputs) && c.outputs[index] != nil {
		return c.outputs[index], nil
	}
	return new(Output), nil
}

type createResource interface {
	Schema(context.Context, fwresource.SchemaRequest, *fwresource.SchemaResponse)
	Create(context.Context, fwresource.CreateRequest, *fwresource.CreateResponse)
}

type updateResource interface {
	Schema(context.Context, fwresource.SchemaRequest, *fwresource.SchemaResponse)
	Update(context.Context, fwresource.UpdateRequest, *fwresource.UpdateResponse)
}

func runCreateWithModel(t *testing.T, resourceUnderTest createResource, model any) *fwresource.CreateResponse {
	t.Helper()

	var schemaResponse fwresource.SchemaResponse
	resourceUnderTest.Schema(t.Context(), fwresource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("resource schema diagnostics: %v", schemaResponse.Diagnostics)
	}
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diagnostics := plan.Set(t.Context(), model); diagnostics.HasError() {
		t.Fatalf("set test plan: %v", diagnostics)
	}
	response := &fwresource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	resourceUnderTest.Create(t.Context(), fwresource.CreateRequest{Plan: plan}, response)
	return response
}

func runUpdateWithModel(t *testing.T, resourceUnderTest updateResource, model any) *fwresource.UpdateResponse {
	t.Helper()

	var schemaResponse fwresource.SchemaResponse
	resourceUnderTest.Schema(t.Context(), fwresource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("resource schema diagnostics: %v", schemaResponse.Diagnostics)
	}
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diagnostics := plan.Set(t.Context(), model); diagnostics.HasError() {
		t.Fatalf("set test plan: %v", diagnostics)
	}
	response := &fwresource.UpdateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	resourceUnderTest.Update(t.Context(), fwresource.UpdateRequest{Plan: plan}, response)
	return response
}

func assertReadbackTimeoutRetainsPlan[Model, PutInput, PutOutput, GetInput, GetOutput any](
	t *testing.T,
	state tfsdk.State,
	hasDiagnosticsError bool,
	want Model,
	put *scriptedS3Call[PutInput, PutOutput],
	get *scriptedS3Call[GetInput, GetOutput],
) {
	t.Helper()

	if !hasDiagnosticsError {
		t.Fatal("write diagnostics contain no error, want readback timeout")
	}
	if len(put.inputs) != 2 || len(get.inputs) != 1 {
		t.Fatalf("S3 calls: Put=%d Get=%d, want 2 and 1", len(put.inputs), len(get.inputs))
	}
	if put.inputs[0] != put.inputs[1] {
		t.Fatal("mutation rebuilt its retry input")
	}
	if len(put.contexts) != 2 || len(get.contexts) != 1 || put.contexts[0] != put.contexts[1] || put.contexts[1] != get.contexts[0] {
		t.Fatal("mutation and readback did not share one propagation context")
	}

	var got Model
	if diagnostics := state.Get(t.Context(), &got); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained state = %#v, want %#v", got, want)
	}
}

func s3PhaseOptionsTimingOutOnSecondWait() s3PhaseOptions {
	waits := 0
	return s3PhaseOptions{
		now:   time.Now,
		delay: func(int) time.Duration { return 0 },
		wait: func(context.Context, time.Duration) error {
			waits++
			if waits >= 2 {
				return context.DeadlineExceeded
			}
			return nil
		},
	}
}

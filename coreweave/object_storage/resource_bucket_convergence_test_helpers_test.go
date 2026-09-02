package objectstorage

import (
	"context"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

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

func assertWriteRetriedAndRetainedState(
	t *testing.T,
	hasDiagnosticsError bool,
	putCalls, getCalls int,
	putContexts, getContexts []context.Context,
) {
	t.Helper()

	if !hasDiagnosticsError {
		t.Fatal("write diagnostics contain no error, want readback timeout")
	}
	if putCalls != 2 || getCalls != 1 {
		t.Fatalf("S3 calls: Put=%d Get=%d, want 2 and 1", putCalls, getCalls)
	}
	if len(putContexts) != 2 || len(getContexts) != 1 || putContexts[0] != putContexts[1] || putContexts[1] != getContexts[0] {
		t.Fatal("mutation and readback did not share one propagation context")
	}
}

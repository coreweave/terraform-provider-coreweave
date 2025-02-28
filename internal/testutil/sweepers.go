package testutil

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	tfprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	tfresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"google.golang.org/protobuf/proto"
)

// GetTerraformResourceName retrieves the resource name for a given resource type as it would be accepted by a default Coreweave terraform provider.
//
// The function first initializes a new instance of the CoreweaveProvider and retrieves its type name.
// Then, it creates an instance of the resource type T, probes its metadata, and retrieves the resource type name.
// The function performs some basic validation on the returned type name, returning an error for anything unexpected or unsafe.
//
// Example usage:
//   resourceName := MustGetTerraformResourceName[MyResourceType]()
//   fmt.Println("Resource name:", resourceName)
//
// Type Parameters:
//   T: A type that implements the tfresource.Resource interface.
//
// Returns:
//   string: The fully qualified resource type name.
func MustGetTerraformResourceName[T tfresource.Resource]() string {
	providerProbe := new(provider.CoreweaveProvider)
	var providerTypeName string
	{
		resp := new(tfprovider.MetadataResponse)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		providerProbe.Metadata(ctx, tfprovider.MetadataRequest{}, resp)
		providerTypeName = resp.TypeName
	}

	if providerTypeName == "" {
		panic("failed to get provider type name")
	}

	var resourceTypeName string
	{
		// This is a bit odd. Because T is an interface, T itself must be a pointer to a type.
		// Thus, the underlying struct of T is two pointers deep, and we need one dereference to get a pointer to the struct.
		var resourceProbe tfresource.Resource = *(new(T))
		resourceProbeResp := new(tfresource.MetadataResponse)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		resourceProbe.Metadata(ctx, tfresource.MetadataRequest{ProviderTypeName: providerTypeName}, resourceProbeResp)
		resourceTypeName = resourceProbeResp.TypeName
	}

	if resourceTypeName == "" {
		panic(fmt.Sprintf("failed to get resource type name for type %s", reflect.TypeFor[T]().String()))
	}
	if !strings.HasPrefix(resourceTypeName, providerTypeName) {
		panic(fmt.Sprintf("resource type name %s does not start with provider type name %s", resourceTypeName, providerTypeName))
	}
	if resourceTypeName == providerTypeName {
		panic(fmt.Sprintf("resource type name %s is the same as provider type name", resourceTypeName))
	}

	return resourceTypeName
}

type CoreweaveSweeper[T proto.Message] struct {
	ListFunc func(ctx context.Context, client *coreweave.Client) ([]T, error)
	GetFunc func(ctx context.Context, client *coreweave.Client, resource T) (T, error)
	DeleteFunc func(ctx context.Context, client *coreweave.Client, resource T) error
	filters []func(T) bool
}

func (cs *CoreweaveSweeper[T]) Check() error {
	if cs.ListFunc == nil {
		return fmt.Errorf("ListFunc is required")
	}
	if cs.GetFunc == nil {
		return fmt.Errorf("GetFunc is required")
	}
	if cs.DeleteFunc == nil {
		return fmt.Errorf("DeleteFunc is required")
	}
	return nil
}

// AddFilter adds a filter function to the sweeper. The filter function should return true if the resource should be deleted, and false otherwise.
func (cs *CoreweaveSweeper[T]) AddFilters(filters ...func(T) bool) {
	cs.filters = append(cs.filters, filters...)
}

// Filter applies all filters to the resource and returns true if the resource should be deleted.
func (cs *CoreweaveSweeper[T]) Filter(resource T) bool {
	if len(cs.filters) == 0 {
		cs.filters = make([]func(T) bool, 0)
	}

	for _, filter := range cs.filters {
		if !filter(resource) {
			return false
		}
	}
	return true
}

func (cs *CoreweaveSweeper[T]) DeleteOne(ctx context.Context, client *coreweave.Client, resource T) error {
	if err := cs.Check(); err != nil {
		return fmt.Errorf("invalid sweeper: %w", err)
	}

	if err := cs.DeleteFunc(ctx, client, resource); err != nil {
		return fmt.Errorf("failed to delete %s: %w", reflect.TypeFor[T]().String(), err)
	}

	resourceTypeString := reflect.TypeFor[T]().String()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for %s to be deleted", resourceTypeString)
		case <-ticker.C:
			_, err := cs.GetFunc(ctx, client, resource)
			if err != nil && connect.CodeOf(err) == connect.CodeNotFound {
				return nil
			} else if err != nil {
				return fmt.Errorf("failed to get %s for unexpected reason: %w", resourceTypeString, err)
			}
		}
	}
}

func (cs *CoreweaveSweeper[T]) Sweep(ctx context.Context, client *coreweave.Client) ([]T, error) {
	resources, err := cs.ListFunc(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	var deletedResources []T
	for _, resource := range resources {
		if cs.Filter(resource) {
			if SweepDryRun() {
				deletedResources = append(deletedResources, resource)
				continue
			}

			if err := cs.DeleteOne(ctx, client, resource); err != nil {
				return deletedResources, fmt.Errorf("failed to delete %s: %w", reflect.TypeFor[T]().String(), err)
			}
			deletedResources = append(deletedResources, resource)
		}
	}

	return deletedResources, nil
}

func SweepDryRun() bool {
	dryRunStr, ok := os.LookupEnv("SWEEP_DRY_RUN")
	if !ok {
		return false
	}
	dryRun, err := strconv.ParseBool(dryRunStr)
	if err != nil {
		panic(fmt.Errorf("failed to parse SWEEP_DRY_RUN as bool: %w", err))
	}
	return dryRun
}

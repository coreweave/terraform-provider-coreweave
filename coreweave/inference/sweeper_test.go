package inference_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const AcceptanceTestPrefix = "test-acc-inf-"

const (
	defaultInferenceModelName          = "meta-llama/Llama-3.1-8B-Instruct"
	defaultInferenceModelBucket        = "infr-cwc38d"
	defaultInferenceModelPath          = "raw/OpenPipe/Llama-3.1-8B-Instruct/a33eb8ed541ad2695fe492718662a3577c929888"
	inferenceDeploymentResourceType    = "coreweave_inference_deployment"
	inferenceCapacityClaimResourceType = "coreweave_inference_capacity_claim"
	inferenceGatewayResourceType       = "coreweave_inference_gateway"
	inferenceDeleteTimeout             = 20 * time.Minute
	inferenceDeletePollInterval        = 10 * time.Second
)

func TestMain(m *testing.M) {
	resource.TestMain(m)
}

func inferenceEnvOrDefault(envVar, defaultValue string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return defaultValue
}

// preferredInferenceZone returns the zone configured via the
// INFR_ZONE env var, or the empty string when unset. When set,
// tests prefer this zone and fail via lifecycle.precondition if the
// zone is not present in the corresponding GetXxxParameters response.
func preferredInferenceZone() string {
	return os.Getenv("INFR_ZONE")
}

// preferredInferenceInstanceType returns the instance type configured via the
// INFR_INSTANCE_ID env var, or the empty string when unset.
// When set, tests prefer this instance type and fail via
// lifecycle.precondition if it is not present in the corresponding
// GetXxxParameters response.
func preferredInferenceInstanceType() string {
	return os.Getenv("INFR_INSTANCE_ID")
}

// inferenceModelName returns the model name configured via the INFR_MODEL_NAME
// env var, or the shared acceptance-test default when unset.
func inferenceModelName() string {
	return inferenceEnvOrDefault("INFR_MODEL_NAME", defaultInferenceModelName)
}

// inferenceModelBucket returns the model bucket configured via the
// INFR_MODEL_BUCKET env var, or the shared acceptance-test default when unset.
func inferenceModelBucket() string {
	return inferenceEnvOrDefault("INFR_MODEL_BUCKET", defaultInferenceModelBucket)
}

// inferenceModelPath returns the model path configured via the INFR_MODEL_PATH
// env var, or the shared acceptance-test default when unset.
func inferenceModelPath() string {
	return inferenceEnvOrDefault("INFR_MODEL_PATH", defaultInferenceModelPath)
}

func normalizeInferenceSweepRegion(region string) (string, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return "", errors.New("inference sweep region must not be empty")
	}
	return region, nil
}

func newInferenceSweepConfig[T any](resourceType, singular, plural string, list func(context.Context) ([]T, error), name func(T) string, id func(T) string, deleteFunc func(context.Context, string) error, waitForDelete func(context.Context, string) error) testutil.SweepConfig[T] {
	return testutil.SweepConfig[T]{
		ResourceType: resourceType,
		List: func(ctx context.Context) ([]T, error) {
			items, err := list(ctx)
			if coreweave.IsNotFoundError(err) {
				return nil, nil
			}
			if err != nil {
				return nil, fmt.Errorf("failed to list %s: %w", plural, err)
			}
			return items, nil
		},
		Name: name,
		Match: func(item T) bool {
			return strings.HasPrefix(name(item), AcceptanceTestPrefix)
		},
		Delete: func(ctx context.Context, item T) error {
			itemID := id(item)
			if err := deleteFunc(ctx, itemID); coreweave.IsNotFoundError(err) {
				return nil
			} else if err != nil {
				return fmt.Errorf("failed to delete %s %s: %w", singular, name(item), err)
			}
			if err := waitForDelete(ctx, itemID); err != nil {
				return fmt.Errorf("timed out waiting for %s %s to be deleted: %w", singular, name(item), err)
			}
			return nil
		},
	}
}

func newDeploymentSweepConfig(client *coreweave.InferenceClient) testutil.SweepConfig[*inferencev1.Deployment] {
	return newInferenceSweepConfig(inferenceDeploymentResourceType, "deployment", "deployments",
		func(ctx context.Context) ([]*inferencev1.Deployment, error) {
			response, err := client.ListDeployments(ctx, connect.NewRequest(&inferencev1.ListDeploymentsRequest{}))
			if err != nil {
				return nil, err
			}
			return response.Msg.GetItems(), nil
		},
		func(item *inferencev1.Deployment) string { return item.GetSpec().GetName() },
		func(item *inferencev1.Deployment) string { return item.GetSpec().GetId() },
		func(ctx context.Context, id string) error {
			_, err := client.DeleteDeployment(ctx, connect.NewRequest(&inferencev1.DeleteDeploymentRequest{Id: id}))
			return err
		}, func(ctx context.Context, id string) error {
			return testutil.WaitForDelete(ctx, inferenceDeleteTimeout, inferenceDeletePollInterval, client.GetDeployment, &inferencev1.GetDeploymentRequest{Id: id})
		})
}

func newCapacityClaimSweepConfig(client *coreweave.InferenceClient) testutil.SweepConfig[*inferencev1.CapacityClaim] {
	return newInferenceSweepConfig(inferenceCapacityClaimResourceType, "capacity claim", "capacity claims",
		func(ctx context.Context) ([]*inferencev1.CapacityClaim, error) {
			response, err := client.ListCapacityClaims(ctx, connect.NewRequest(&inferencev1.ListCapacityClaimsRequest{}))
			if err != nil {
				return nil, err
			}
			return response.Msg.GetCapacityClaims(), nil
		},
		func(item *inferencev1.CapacityClaim) string { return item.GetSpec().GetName() },
		func(item *inferencev1.CapacityClaim) string { return item.GetSpec().GetId() },
		func(ctx context.Context, id string) error {
			_, err := client.DeleteCapacityClaim(ctx, connect.NewRequest(&inferencev1.DeleteCapacityClaimRequest{Id: id}))
			return err
		}, func(ctx context.Context, id string) error {
			return testutil.WaitForDelete(ctx, inferenceDeleteTimeout, inferenceDeletePollInterval, client.GetCapacityClaim, &inferencev1.GetCapacityClaimRequest{Id: id})
		})
}

func newGatewaySweepConfig(client *coreweave.InferenceClient) testutil.SweepConfig[*inferencev1.Gateway] {
	return newInferenceSweepConfig(inferenceGatewayResourceType, "gateway", "gateways",
		func(ctx context.Context) ([]*inferencev1.Gateway, error) {
			response, err := client.ListGateways(ctx, connect.NewRequest(&inferencev1.ListGatewaysRequest{}))
			if err != nil {
				return nil, err
			}
			return response.Msg.GetItems(), nil
		},
		func(item *inferencev1.Gateway) string { return item.GetSpec().GetName() },
		func(item *inferencev1.Gateway) string { return item.GetSpec().GetId() },
		func(ctx context.Context, id string) error {
			_, err := client.DeleteGateway(ctx, connect.NewRequest(&inferencev1.DeleteGatewayRequest{Id: id}))
			return err
		}, func(ctx context.Context, id string) error {
			return testutil.WaitForDelete(ctx, inferenceDeleteTimeout, inferenceDeletePollInterval, client.GetGateway, &inferencev1.GetGatewayRequest{Id: id})
		})
}

func newInferenceSweeper(resourceType string, dependencies []string, config func(context.Context, testutil.SweepRuntime, *coreweave.InferenceClient) error) *resource.Sweeper {
	return &resource.Sweeper{
		Name:         resourceType,
		Dependencies: dependencies,
		F: func(region string) error {
			if _, err := normalizeInferenceSweepRegion(region); err != nil {
				return err
			}
			runtime, err := testutil.SweepRuntimeFromEnv()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}
			return config(ctx, runtime, client.Inference)
		},
	}
}

func newDeploymentSweeper() *resource.Sweeper {
	return newInferenceSweeper(inferenceDeploymentResourceType, []string{}, func(ctx context.Context, runtime testutil.SweepRuntime, client *coreweave.InferenceClient) error {
		return testutil.Sweep(ctx, runtime, newDeploymentSweepConfig(client))
	})
}

func newCapacityClaimSweeper() *resource.Sweeper {
	return newInferenceSweeper(inferenceCapacityClaimResourceType, []string{inferenceDeploymentResourceType}, func(ctx context.Context, runtime testutil.SweepRuntime, client *coreweave.InferenceClient) error {
		return testutil.Sweep(ctx, runtime, newCapacityClaimSweepConfig(client))
	})
}

func newGatewaySweeper() *resource.Sweeper {
	return newInferenceSweeper(inferenceGatewayResourceType, []string{inferenceDeploymentResourceType}, func(ctx context.Context, runtime testutil.SweepRuntime, client *coreweave.InferenceClient) error {
		return testutil.Sweep(ctx, runtime, newGatewaySweepConfig(client))
	})
}

func init() {
	resource.AddTestSweepers(inferenceDeploymentResourceType, newDeploymentSweeper())
	resource.AddTestSweepers(inferenceCapacityClaimResourceType, newCapacityClaimSweeper())
	resource.AddTestSweepers(inferenceGatewayResourceType, newGatewaySweeper())
}

func TestInferenceSweeperRegistrations(t *testing.T) {
	tests := []struct {
		name         string
		dependencies []string
		sweeper      *resource.Sweeper
	}{
		{name: inferenceDeploymentResourceType, dependencies: []string{}, sweeper: newDeploymentSweeper()},
		{name: inferenceCapacityClaimResourceType, dependencies: []string{inferenceDeploymentResourceType}, sweeper: newCapacityClaimSweeper()},
		{name: inferenceGatewayResourceType, dependencies: []string{inferenceDeploymentResourceType}, sweeper: newGatewaySweeper()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.name, tt.sweeper.Name)
			assert.Equal(t, tt.dependencies, tt.sweeper.Dependencies)
			assert.NotNil(t, tt.sweeper.F)
		})
	}
}

func TestInferenceSweeperValidationPrecedesSetup(t *testing.T) {
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "")
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, "restored by testing")
	require.NoError(t, os.Unsetenv(provider.CoreweaveApiEndpointEnvVar))
	t.Setenv("TEST_ACC_SWEEP_PARALLEL", "invalid")

	require.EqualError(t, newDeploymentSweeper().F(" \t\n"), "inference sweep region must not be empty")
	assert.ErrorContains(t, newCapacityClaimSweeper().F("zone-a"), "parse TEST_ACC_SWEEP_PARALLEL as integer")
	_, found := os.LookupEnv(provider.CoreweaveApiEndpointEnvVar)
	require.False(t, found, "client setup must not run before region and runtime validation")
}

func TestInferenceSweepConfigPolicy(t *testing.T) {
	const listedID = "listed-id"

	type item struct {
		name string
		id   string
	}

	listed := &item{name: AcceptanceTestPrefix + "selected", id: listedID}
	var listError, deleteError, waitError error
	var deletedIDs, waitedIDs []string
	config := newInferenceSweepConfig("test_resource", "resource", "resources",
		func(context.Context) ([]*item, error) { return []*item{listed}, listError },
		func(item *item) string { return item.name },
		func(item *item) string { return item.id },
		func(_ context.Context, id string) error {
			deletedIDs = append(deletedIDs, id)
			return deleteError
		},
		func(_ context.Context, id string) error {
			waitedIDs = append(waitedIDs, id)
			return waitError
		},
	)

	assert.True(t, config.Match(listed))
	assert.False(t, config.Match(&item{name: "production"}))
	items, err := config.List(t.Context())
	require.NoError(t, err)
	require.Equal(t, []*item{listed}, items)

	listError = connect.NewError(connect.CodeNotFound, assert.AnError)
	items, err = config.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, items)
	listError = assert.AnError
	_, err = config.List(t.Context())
	assert.ErrorContains(t, err, "failed to list resources")
	listError = nil

	require.NoError(t, config.Delete(t.Context(), listed))
	assert.Equal(t, []string{listedID}, deletedIDs)
	assert.Equal(t, []string{listedID}, waitedIDs)

	deleteError = connect.NewError(connect.CodeNotFound, assert.AnError)
	waitedIDs = nil
	require.NoError(t, config.Delete(t.Context(), listed))
	assert.Empty(t, waitedIDs)

	deleteError = assert.AnError
	assert.ErrorContains(t, config.Delete(t.Context(), listed), "failed to delete resource")

	deleteError = nil
	waitError = assert.AnError
	assert.ErrorContains(t, config.Delete(t.Context(), listed), "timed out waiting for resource")
}

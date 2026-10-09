package containerregistry

import (
	"context"
	"errors"
	"math"
	"math/big"
	"testing"
	"time"

	client "buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/v2/coreweave/registry/v1alpha1/registryv1alpha1connect"
	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"connectrpc.com/connect/v2"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

type pollingClient struct {
	client.RegistryServiceClient
	access     []*api.RegistryAccessConfiguration
	lifecycle  []*api.RegistryLifecyclePolicy
	pages      []*api.ListRegistryNamespacesResponse
	operations []*longrunningpb.Operation
	calls      int
	tokens     []string
}

// GetRegistryAccessConfiguration returns scripted rollout observations.
func (c *pollingClient) GetRegistryAccessConfiguration(context.Context, *api.GetRegistryAccessConfigurationRequest) (*api.RegistryAccessConfiguration, error) {
	i := min(c.calls, len(c.access)-1)
	c.calls++
	return c.access[i], nil
}

// GetRegistryLifecyclePolicy returns scripted home-region acknowledgements.
func (c *pollingClient) GetRegistryLifecyclePolicy(context.Context, *api.GetRegistryLifecyclePolicyRequest) (*api.RegistryLifecyclePolicy, error) {
	i := min(c.calls, len(c.lifecycle)-1)
	c.calls++
	return c.lifecycle[i], nil
}

// ListRegistryNamespaces records opaque tokens and page-size limits.
func (c *pollingClient) ListRegistryNamespaces(_ context.Context, q *api.ListRegistryNamespacesRequest) (*api.ListRegistryNamespacesResponse, error) {
	if q.PageSize != 200 {
		return nil, errors.New("unexpected page size")
	}
	c.tokens = append(c.tokens, q.PageToken)
	i := min(c.calls, len(c.pages)-1)
	c.calls++
	return c.pages[i], nil
}

// GetOperation returns scripted operation states.
func (c *pollingClient) GetOperation(context.Context, *api.RegistryServiceGetOperationRequest) (*longrunningpb.Operation, error) {
	i := min(c.calls, len(c.operations)-1)
	c.calls++
	return c.operations[i], nil
}

// TestExactRevisionWaits rejects newer content even if that content was accepted.
func TestExactRevisionWaits(t *testing.T) {
	accepted := api.RegistryAccessConfiguration_ACCESS_CONFIG_STATE_ACCEPTED
	partial := api.RegistryAccessConfiguration_ACCESS_CONFIG_STATE_PARTIALLY_ACCEPTED
	t.Run("partial then accepted", func(t *testing.T) {
		c := &pollingClient{access: []*api.RegistryAccessConfiguration{{Revision: 2, AccessConfigState: partial}, {Revision: 2, AccessConfigState: accepted}}}
		_, e := waitAccess(t.Context(), c, "namespaces/example-images", 2)
		require.NoError(t, e)
		require.Equal(t, 2, c.calls)
	})
	t.Run("competing access", func(t *testing.T) {
		c := &pollingClient{access: []*api.RegistryAccessConfiguration{{Revision: 3, AccessConfigState: accepted}}}
		_, e := waitAccess(t.Context(), c, "namespaces/example-images", 2)
		require.ErrorContains(t, e, "competing writer")
	})
	t.Run("no-op lifecycle unacknowledged", func(t *testing.T) {
		c := &pollingClient{lifecycle: []*api.RegistryLifecyclePolicy{{Revision: 2}}}
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		defer cancel()
		_, e := waitLifecycle(ctx, c, "namespaces/example-images", 2)
		require.ErrorContains(t, e, "not acknowledged")
	})
	t.Run("competing lifecycle", func(t *testing.T) {
		rev := int64(3)
		c := &pollingClient{lifecycle: []*api.RegistryLifecyclePolicy{{Revision: 3, AppliedRevision: &rev}}}
		_, e := waitLifecycle(t.Context(), c, "namespaces/example-images", 2)
		require.ErrorContains(t, e, "competing writer")
	})
}

// TestOperationCompletion verifies immediate, error, wrong-type, cancellation and normal polling behavior.
func TestOperationCompletion(t *testing.T) {
	a, e := anypb.New(&emptypb.Empty{})
	require.NoError(t, e)
	done := &longrunningpb.Operation{Done: true, Result: &longrunningpb.Operation_Response{Response: a}}
	c := &pollingClient{}
	require.NoError(t, waitOperation(t.Context(), c, done, &emptypb.Empty{}))
	require.Zero(t, c.calls)
	require.Error(t, waitOperation(t.Context(), c, done, &api.RegistryNamespace{}))
	failed := &longrunningpb.Operation{Done: true, Result: &longrunningpb.Operation_Error{Error: &status.Status{Code: 9, Message: "nonempty"}}}
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(waitOperation(t.Context(), c, failed, &emptypb.Empty{})))
	name := "namespaces/example-images/operations/00000000-0000-4000-8000-000000000000"
	done.Name = name
	c.operations = []*longrunningpb.Operation{done}
	require.NoError(t, waitOperation(t.Context(), c, &longrunningpb.Operation{Name: name}, &emptypb.Empty{}))
	require.Equal(t, 1, c.calls)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, waitOperation(ctx, c, &longrunningpb.Operation{Name: name}, &emptypb.Empty{}), context.Canceled)
}

// TestPagination rejects repeated tokens and duplicate objects, and sorts complete results.
func TestPagination(t *testing.T) {
	c := &pollingClient{pages: []*api.ListRegistryNamespacesResponse{{RegistryNamespaces: []*api.RegistryNamespace{{Name: "namespaces/zzz"}}, NextPageToken: "opaque+/="}, {RegistryNamespaces: []*api.RegistryNamespace{{Name: "namespaces/aaa"}}}}}
	out, e := listNamespaces(t.Context(), c)
	require.NoError(t, e)
	require.Equal(t, "namespaces/aaa", out[0].Name)
	require.Equal(t, []string{"", "opaque+/="}, c.tokens)
	c = &pollingClient{pages: []*api.ListRegistryNamespacesResponse{{NextPageToken: "loop"}}}
	_, e = listNamespaces(t.Context(), c)
	require.ErrorContains(t, e, "repeated")
	c = &pollingClient{pages: []*api.ListRegistryNamespacesResponse{{RegistryNamespaces: []*api.RegistryNamespace{{Name: "namespaces/aaa"}, {Name: "namespaces/aaa"}}}}}
	_, e = listNamespaces(t.Context(), c)
	require.ErrorContains(t, e, "duplicate")
}

// TestCanonicalInputs verifies lossless uint64 observations, CIDR filters, and bounded durations.
func TestCanonicalInputs(t *testing.T) {
	for _, s := range []string{"1s", "0.100s", "2592000s"} {
		_, e := parseAge(s)
		require.NoError(t, e)
	}
	for _, s := range []string{"0s", "-1s", "1h", "0.1s", "9223372037s"} {
		_, e := parseAge(s)
		require.Error(t, e, s)
	}
	var v NamespaceResourceModel
	require.Empty(t, v.Set(t.Context(), &api.RegistryNamespace{ContentStatus: &api.RegistryNamespaceContentStatus{UsageBytes: math.MaxUint64}}))
	number := v.ContentStatus.Attributes()["usage_bytes"].(types.Number).ValueBigFloat()
	n, _ := number.Int(nil)
	require.Equal(t, new(big.Int).SetUint64(math.MaxUint64), n)
	zones, diags := types.SetValueFrom(t.Context(), types.StringType, []string{"us-lab-01a", "US-LAB-01A"})
	require.False(t, diags.HasError())
	_, e := zoneFilters(zones)
	require.ErrorContains(t, e, "duplicate")
	_, e = zoneFilters(types.SetUnknown(types.StringType))
	require.ErrorContains(t, e, "known")
}

// TestAccessRoundTripAndValidation preserves CEL bytes and validates allow/deny composition.
func TestAccessRoundTripAndValidation(t *testing.T) {
	p := &api.RegistryAccessConfiguration{PolicySets: []*api.RegistryAccessPolicySet{{Id: "readers", IdentitySelector: api.RegistryIdentitySelector(api.RegistryIdentitySelector_value["REGISTRY_IDENTITY_SELECTOR_COREWEAVE_KUBERNETES"]), Rules: []*api.RegistryAccessRule{{Id: "pull", Expression: " true \n"}}}}, RequestIpAcl: &api.RegistryIpAcl{AllowCidrs: []string{"2001:db8::/32"}}}
	var a AccessConfigurationResourceModel
	require.Empty(t, a.Set(t.Context(), p))
	out, e := accessInput(t.Context(), a.PolicySets, a.RequestIPACL, path.Empty())
	require.Empty(t, e)
	equal, err := equalAccess(t.Context(), p, out)
	require.NoError(t, err)
	require.True(t, equal)
	require.Equal(t, " true \n", out.PolicySets[0].Rules[0].Expression)
	for _, cidr := range []string{"192.0.2.1/24", "::ffff:192.0.2.0/120", "2001:0db8::/32", "bad"} {
		p.RequestIpAcl.AllowCidrs = []string{cidr}
		require.Empty(t, a.Set(t.Context(), p))
		_, e = accessInput(t.Context(), a.PolicySets, a.RequestIPACL, path.Empty())
		require.True(t, e.HasError(), cidr)
	}
	p.RequestIpAcl = &api.RegistryIpAcl{}
	require.Empty(t, a.Set(t.Context(), p))
	_, e = accessInput(t.Context(), a.PolicySets, a.RequestIPACL, path.Empty())
	require.True(t, e.HasError())
}

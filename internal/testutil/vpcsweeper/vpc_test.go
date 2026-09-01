package vpcsweeper_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil/vpcsweeper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClient struct {
	listError      error
	deleteError    error
	getError       error
	deletedIDs     []string
	requestedGetID string
}

const (
	listedID   = "listed-id"
	testPrefix = "test-"
	testVPC    = "test-one"
	testZone   = "zone-a"
)

func (client *fakeClient) ListVPCs(context.Context, *connect.Request[networkingv1beta1.ListVPCsRequest]) (*connect.Response[networkingv1beta1.ListVPCsResponse], error) {
	if client.listError != nil {
		return nil, client.listError
	}
	return connect.NewResponse(&networkingv1beta1.ListVPCsResponse{}), nil
}

func (client *fakeClient) DeleteVPC(_ context.Context, request *connect.Request[networkingv1beta1.DeleteVPCRequest]) (*connect.Response[networkingv1beta1.DeleteVPCResponse], error) {
	client.deletedIDs = append(client.deletedIDs, request.Msg.GetId())
	if client.deleteError != nil {
		return nil, client.deleteError
	}
	return connect.NewResponse(&networkingv1beta1.DeleteVPCResponse{}), nil
}

func (client *fakeClient) GetVPC(_ context.Context, request *connect.Request[networkingv1beta1.GetVPCRequest]) (*connect.Response[networkingv1beta1.GetVPCResponse], error) {
	client.requestedGetID = request.Msg.GetId()
	if client.getError != nil {
		return nil, client.getError
	}
	return connect.NewResponse(&networkingv1beta1.GetVPCResponse{}), nil
}

func TestNewValidatesSelectors(t *testing.T) {
	tests := []struct {
		name      string
		config    vpcsweeper.Config
		wantError string
	}{
		{name: "empty prefix", config: vpcsweeper.Config{Zone: testZone}, wantError: "prefix must not be empty"},
		{name: "whitespace prefix", config: vpcsweeper.Config{Prefix: " \t\n", Zone: testZone}, wantError: "prefix must not be empty"},
		{name: "empty zone", config: vpcsweeper.Config{Prefix: testPrefix}, wantError: "zone must not be empty"},
		{name: "whitespace zone", config: vpcsweeper.Config{Prefix: testPrefix, Zone: " \t\n"}, wantError: "zone must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := vpcsweeper.New(&fakeClient{}, tt.config)
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestConfigMatchesPrefixAndZone(t *testing.T) {
	tests := []struct {
		name string
		vpc  *networkingv1beta1.VPC
		want bool
	}{
		{name: "both match", vpc: &networkingv1beta1.VPC{Name: testVPC, Zone: testZone}, want: true},
		{name: "prefix mismatch", vpc: &networkingv1beta1.VPC{Name: "prod-one", Zone: testZone}},
		{name: "zone mismatch", vpc: &networkingv1beta1.VPC{Name: testVPC, Zone: "zone-b"}},
		{name: "both mismatch", vpc: &networkingv1beta1.VPC{Name: "prod-one", Zone: "zone-b"}},
	}
	config, err := vpcsweeper.New(&fakeClient{}, vpcsweeper.Config{Prefix: "  " + testPrefix + "\n", Zone: "\t" + testZone + "  "})
	require.NoError(t, err)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, config.Match(tt.vpc))
		})
	}
}

func TestConfigListError(t *testing.T) {
	config, err := vpcsweeper.New(&fakeClient{listError: errors.New("unavailable")}, vpcsweeper.Config{Prefix: testPrefix, Zone: testZone})
	require.NoError(t, err)
	_, err = config.List(t.Context())
	require.EqualError(t, err, "list VPCs: unavailable")
}

func TestConfigDeletesListedID(t *testing.T) {
	tests := []struct {
		name      string
		client    *fakeClient
		wantError string
		wantGet   bool
	}{
		{name: "delete not found succeeds", client: &fakeClient{deleteError: connect.NewError(connect.CodeNotFound, errors.New("gone"))}},
		{name: "delete failure", client: &fakeClient{deleteError: errors.New("unavailable")}, wantError: "delete VPC: unavailable"},
		{name: "post-delete polling succeeds", client: &fakeClient{getError: connect.NewError(connect.CodeNotFound, errors.New("gone"))}, wantGet: true},
		{name: "post-delete polling fails", client: &fakeClient{getError: connect.NewError(connect.CodeUnavailable, errors.New("API unavailable"))}, wantError: "wait for VPC deletion", wantGet: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := vpcsweeper.New(tt.client, vpcsweeper.Config{Prefix: testPrefix, Zone: testZone})
			require.NoError(t, err)
			err = config.Delete(t.Context(), &networkingv1beta1.VPC{Id: listedID, Name: testVPC, Zone: testZone})
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
			assert.Equal(t, []string{listedID}, tt.client.deletedIDs)
			if tt.wantGet {
				assert.Equal(t, listedID, tt.client.requestedGetID)
			} else {
				assert.Empty(t, tt.client.requestedGetID)
			}
		})
	}
}

package serviceaccount_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"connectrpc.com/connect/v2"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	serviceAccountResourceType      = "coreweave_iam_service_account"
	serviceAccountSweepTimeout      = 10 * time.Minute
	serviceAccountPageSize          = int32(200)
	testServiceAccountParent        = "organizations/a1b2c3"
	testServiceAccountNextPageToken = "next"
)

var acceptanceServiceAccountIDPattern = regexp.MustCompile(`^tf-acc-sa-[0-9]+$`)

type serviceAccountSweepClient interface {
	ListServiceAccounts(context.Context, *directoryv1alpha.ListServiceAccountsRequest) (*directoryv1alpha.ListServiceAccountsResponse, error)
	DeleteServiceAccount(context.Context, *directoryv1alpha.DeleteServiceAccountRequest) (*emptypb.Empty, error)
}

func listServiceAccounts(ctx context.Context, client serviceAccountSweepClient, parent string) ([]*directoryv1alpha.ServiceAccount, error) {
	var accounts []*directoryv1alpha.ServiceAccount
	pageToken := ""
	seenTokens := make(map[string]struct{})
	for {
		request := &directoryv1alpha.ListServiceAccountsRequest{}
		request.SetParent(parent)
		request.SetMaxPageSize(serviceAccountPageSize)
		if pageToken != "" {
			request.SetPageToken(pageToken)
		}
		response, err := client.ListServiceAccounts(ctx, request)
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("Directory API returned an empty service account list response")
		}
		accounts = append(accounts, response.GetServiceAccounts()...)
		nextToken := response.GetNextPageToken()
		if nextToken == "" {
			return accounts, nil
		}
		if _, exists := seenTokens[nextToken]; exists {
			return nil, fmt.Errorf("Directory API repeated service account page token %q", nextToken)
		}
		seenTokens[nextToken] = struct{}{}
		pageToken = nextToken
	}
}

func serviceAccountSweepTarget(account *directoryv1alpha.ServiceAccount, parent string) bool {
	if account == nil || account.GetSystemManaged() {
		return false
	}
	prefix := parent + "/serviceAccounts/"
	name := account.GetName()
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return false
	}
	return acceptanceServiceAccountIDPattern.MatchString(name[len(prefix):])
}

func newServiceAccountSweepConfig(client serviceAccountSweepClient, parent string) testutil.SweepConfig[*directoryv1alpha.ServiceAccount] {
	return testutil.SweepConfig[*directoryv1alpha.ServiceAccount]{
		ResourceType: serviceAccountResourceType,
		CreatedAt:    (*directoryv1alpha.ServiceAccount).GetCreateTime,
		List: func(ctx context.Context) ([]*directoryv1alpha.ServiceAccount, error) {
			return listServiceAccounts(ctx, client, parent)
		},
		Name: func(account *directoryv1alpha.ServiceAccount) string {
			return account.GetName()
		},
		Match: func(account *directoryv1alpha.ServiceAccount) bool {
			return serviceAccountSweepTarget(account, parent)
		},
		Delete: func(ctx context.Context, account *directoryv1alpha.ServiceAccount) error {
			name := account.GetName()
			_, err := client.DeleteServiceAccount(ctx, &directoryv1alpha.DeleteServiceAccountRequest{Name: &name})
			if coreweave.IsNotFoundError(err) {
				return nil
			}
			return err
		},
	}
}

func sweepServiceAccounts(_ string) error {
	runtime, err := testutil.SweepRuntimeFromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), serviceAccountSweepTimeout)
	defer cancel()

	testutil.SetEnvDefaults()
	client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
	if err != nil {
		return fmt.Errorf("build client: %w", err)
	}
	identity, err := client.GetCallerIdentity(ctx)
	if err != nil {
		return fmt.Errorf("get caller identity: %w", err)
	}
	parent := "organizations/" + identity.OrganizationID
	return testutil.Sweep(ctx, runtime, newServiceAccountSweepConfig(client.Directory, parent))
}

func init() {
	resource.AddTestSweepers(serviceAccountResourceType, &resource.Sweeper{
		Name:         serviceAccountResourceType,
		Dependencies: []string{},
		F:            sweepServiceAccounts,
	})
}

func TestMain(m *testing.M) {
	resource.TestMain(m)
}

type serviceAccountListCall struct {
	parent, pageToken string
	pageSize          int32
}

type fakeServiceAccountSweepClient struct {
	pages        map[string]*directoryv1alpha.ListServiceAccountsResponse
	listError    error
	deleteErrors map[string]error
	listCalls    []serviceAccountListCall
	deleted      []string
}

func (f *fakeServiceAccountSweepClient) ListServiceAccounts(_ context.Context, request *directoryv1alpha.ListServiceAccountsRequest) (*directoryv1alpha.ListServiceAccountsResponse, error) {
	f.listCalls = append(f.listCalls, serviceAccountListCall{
		parent: request.GetParent(), pageToken: request.GetPageToken(), pageSize: request.GetMaxPageSize(),
	})
	if f.listError != nil {
		return nil, f.listError
	}
	return f.pages[request.GetPageToken()], nil
}

func (f *fakeServiceAccountSweepClient) DeleteServiceAccount(_ context.Context, request *directoryv1alpha.DeleteServiceAccountRequest) (*emptypb.Empty, error) {
	name := request.GetName()
	f.deleted = append(f.deleted, name)
	return &emptypb.Empty{}, f.deleteErrors[name]
}

func testServiceAccount(name string, createdAt time.Time) *directoryv1alpha.ServiceAccount {
	return &directoryv1alpha.ServiceAccount{Name: name, CreateTime: timestamppb.New(createdAt)}
}

func TestServiceAccountSweepTarget(t *testing.T) {
	t.Parallel()
	parent := testServiceAccountParent
	systemManaged := testServiceAccount(parent+"/serviceAccounts/tf-acc-sa-123", time.Now())
	systemManaged.SetSystemManaged(true)
	tests := []struct {
		name    string
		account *directoryv1alpha.ServiceAccount
		want    bool
	}{
		{name: "acceptance account", account: testServiceAccount(parent+"/serviceAccounts/tf-acc-sa-123", time.Now()), want: true},
		{name: "different organization", account: testServiceAccount("organizations/z9y8x7/serviceAccounts/tf-acc-sa-123", time.Now())},
		{name: "production account", account: testServiceAccount(parent+"/serviceAccounts/deployer", time.Now())},
		{name: "partial suffix match", account: testServiceAccount(parent+"/serviceAccounts/tf-acc-sa-123-extra", time.Now())},
		{name: "system managed", account: systemManaged},
		{name: "missing account"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, serviceAccountSweepTarget(test.account, parent))
		})
	}
}

func TestListServiceAccountsPaginates(t *testing.T) {
	t.Parallel()
	parent := testServiceAccountParent
	first := testServiceAccount(parent+"/serviceAccounts/tf-acc-sa-1", time.Now())
	second := testServiceAccount(parent+"/serviceAccounts/tf-acc-sa-2", time.Now())
	fake := &fakeServiceAccountSweepClient{pages: map[string]*directoryv1alpha.ListServiceAccountsResponse{
		"":                              {ServiceAccounts: []*directoryv1alpha.ServiceAccount{first}, NextPageToken: pointer(testServiceAccountNextPageToken)},
		testServiceAccountNextPageToken: {ServiceAccounts: []*directoryv1alpha.ServiceAccount{second}},
	}}

	accounts, err := listServiceAccounts(t.Context(), fake, parent)
	require.NoError(t, err)
	assert.Equal(t, []*directoryv1alpha.ServiceAccount{first, second}, accounts)
	assert.Equal(t, []serviceAccountListCall{
		{parent: parent, pageSize: serviceAccountPageSize},
		{parent: parent, pageToken: testServiceAccountNextPageToken, pageSize: serviceAccountPageSize},
	}, fake.listCalls)
}

func TestListServiceAccountsRejectsRepeatedPageToken(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceAccountSweepClient{pages: map[string]*directoryv1alpha.ListServiceAccountsResponse{
		"":                              {NextPageToken: pointer(testServiceAccountNextPageToken)},
		testServiceAccountNextPageToken: {NextPageToken: pointer(testServiceAccountNextPageToken)},
	}}
	_, err := listServiceAccounts(t.Context(), fake, testServiceAccountParent)
	require.ErrorContains(t, err, `repeated service account page token "next"`)
}

func TestServiceAccountSweepConfigDeletesOnlyStaleAcceptanceAccounts(t *testing.T) {
	t.Parallel()
	parent := testServiceAccountParent
	oldTarget := testServiceAccount(parent+"/serviceAccounts/tf-acc-sa-1", time.Now().Add(-24*time.Hour))
	youngTarget := testServiceAccount(parent+"/serviceAccounts/tf-acc-sa-2", time.Now())
	production := testServiceAccount(parent+"/serviceAccounts/deployer", time.Now().Add(-24*time.Hour))
	fake := &fakeServiceAccountSweepClient{pages: map[string]*directoryv1alpha.ListServiceAccountsResponse{
		"": {ServiceAccounts: []*directoryv1alpha.ServiceAccount{oldTarget, youngTarget, production}},
	}}

	err := testutil.Sweep(t.Context(), testutil.SweepRuntime{Parallelism: 1, MinAge: 2 * time.Hour}, newServiceAccountSweepConfig(fake, parent))
	require.NoError(t, err)
	assert.Equal(t, []string{oldTarget.GetName()}, fake.deleted)
}

func TestServiceAccountSweepDeleteIgnoresNotFound(t *testing.T) {
	t.Parallel()
	parent := testServiceAccountParent
	account := testServiceAccount(parent+"/serviceAccounts/tf-acc-sa-1", time.Now().Add(-24*time.Hour))
	fake := &fakeServiceAccountSweepClient{
		pages: map[string]*directoryv1alpha.ListServiceAccountsResponse{"": {}},
		deleteErrors: map[string]error{
			account.GetName(): connect.Errorf(connect.CodeNotFound, "already deleted"),
		},
	}
	require.NoError(t, newServiceAccountSweepConfig(fake, parent).Delete(t.Context(), account))
}

func pointer[T any](value T) *T {
	return &value
}

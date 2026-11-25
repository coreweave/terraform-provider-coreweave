package serviceaccount_test

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const serviceAccountDataSourceAddress = "data.coreweave_iam_service_account.test"

func serviceAccountDataSourceTestConfig(endpoint, organizationID, id string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

data "coreweave_iam_service_account" "test" {
  organization_id = %q
  id              = %q
}
`, endpoint, organizationID, id)
}

func seedServiceAccount(service *serviceAccountTestServer, account *directoryv1alpha.ServiceAccount) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.accounts[account.GetName()] = cloneServiceAccount(account)
}

func TestServiceAccountDataSourceLookup(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	createdAt := time.Date(2026, time.November, 25, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	seedServiceAccount(service, &directoryv1alpha.ServiceAccount{
		Name:          "organizations/a1b2c3/serviceAccounts/ci-agent",
		Uid:           proto.String("c4f985e4-a2f0-44f8-937e-000000000001"),
		OrgId:         proto.String("a1b2c3"),
		Creator:       proto.String("organizations/a1b2c3/users/test-user"),
		DisplayName:   proto.String("CI agent"),
		SystemManaged: proto.Bool(false),
		Active:        proto.Bool(true),
		CreateTime:    timestamppb.New(createdAt),
		UpdateTime:    timestamppb.New(updatedAt),
	})

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{{
			Config: serviceAccountDataSourceTestConfig(server.URL, "a1b2c3", "ci-agent"),
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttr(serviceAccountDataSourceAddress, "id", "ci-agent"),
				tfresource.TestCheckResourceAttr(serviceAccountDataSourceAddress, "organization_id", "a1b2c3"),
				tfresource.TestCheckResourceAttr(serviceAccountDataSourceAddress, "name", "organizations/a1b2c3/serviceAccounts/ci-agent"),
				tfresource.TestCheckResourceAttr(serviceAccountDataSourceAddress, "uid", "c4f985e4-a2f0-44f8-937e-000000000001"),
				tfresource.TestCheckResourceAttr(serviceAccountDataSourceAddress, "creator", "organizations/a1b2c3/users/test-user"),
				tfresource.TestCheckResourceAttr(serviceAccountDataSourceAddress, "display_name", "CI agent"),
				tfresource.TestCheckResourceAttr(serviceAccountDataSourceAddress, "system_managed", "false"),
				tfresource.TestCheckResourceAttr(serviceAccountDataSourceAddress, "active", "true"),
				tfresource.TestCheckResourceAttr(serviceAccountDataSourceAddress, "created_at", createdAt.Format(time.RFC3339)),
				tfresource.TestCheckResourceAttr(serviceAccountDataSourceAddress, "updated_at", updatedAt.Format(time.RFC3339)),
			),
		}},
	})
}

func TestServiceAccountDataSourceDiagnostics(t *testing.T) {
	tests := map[string]struct {
		organizationID string
		id             string
		account        *directoryv1alpha.ServiceAccount
		wantError      string
	}{
		"invalid organization ID": {
			organizationID: "INVALID",
			id:             "ci-agent",
			wantError:      "Invalid Attribute Value Match",
		},
		"invalid service account ID": {
			organizationID: "a1b2c3",
			id:             "CI_AGENT",
			wantError:      "Invalid Attribute Value Match",
		},
		"not found": {
			organizationID: "a1b2c3",
			id:             "missing",
			wantError:      "Service Account Not Found",
		},
		"invalid API response": {
			organizationID: "a1b2c3",
			id:             "ci-agent",
			account: &directoryv1alpha.ServiceAccount{
				Name: "organizations/a1b2c3/serviceAccounts/ci-agent",
			},
			wantError: "service account UID is missing",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			service, server := startServiceAccountTestServer(t)
			if test.account != nil {
				seedServiceAccount(service, test.account)
			}
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				Steps: []tfresource.TestStep{{
					Config:      serviceAccountDataSourceTestConfig(server.URL, test.organizationID, test.id),
					ExpectError: regexp.MustCompile(test.wantError),
				}},
			})
		})
	}
}

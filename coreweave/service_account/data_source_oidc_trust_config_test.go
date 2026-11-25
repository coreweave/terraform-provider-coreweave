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

const oidcTrustConfigDataSourceAddress = "data.coreweave_iam_service_account_oidc_trust_config.test"

func oidcTrustConfigDataSourceTestConfig(endpoint, organizationID, serviceAccountID, id string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

data "coreweave_iam_service_account_oidc_trust_config" "test" {
  organization_id    = %q
  service_account_id = %q
  id                 = %q
}
`, endpoint, organizationID, serviceAccountID, id)
}

func seedOIDCTrustConfig(service *oidcTrustConfigTestServer, config *directoryv1alpha.ServiceAccountOidcTrustConfig) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.configs[config.GetName()] = cloneOIDCTrustConfig(config)
}

func TestOIDCTrustConfigDataSourceLookup(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	createdAt := time.Date(2026, time.November, 25, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	seedOIDCTrustConfig(service, &directoryv1alpha.ServiceAccountOidcTrustConfig{
		Name:                            "organizations/a1b2c3/serviceAccounts/ci-agent/oidcTrustConfigs/github-actions",
		Uid:                             proto.String("74931f1b-b5cc-48d4-9a97-24073d43fd82"),
		Description:                     proto.String("GitHub Actions deployments"),
		Cel:                             proto.String(`token.repository == "coreweave/example"`),
		WorkloadFederationOidcConfigUid: proto.String("13db6848-17e8-42b0-8615-4d3fc86bd721"),
		CreateTime:                      timestamppb.New(createdAt),
		UpdateTime:                      timestamppb.New(updatedAt),
	})

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{{
			Config: oidcTrustConfigDataSourceTestConfig(server.URL, "a1b2c3", "ci-agent", "github-actions"),
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttr(oidcTrustConfigDataSourceAddress, "id", "github-actions"),
				tfresource.TestCheckResourceAttr(oidcTrustConfigDataSourceAddress, "organization_id", "a1b2c3"),
				tfresource.TestCheckResourceAttr(oidcTrustConfigDataSourceAddress, "service_account_id", "ci-agent"),
				tfresource.TestCheckResourceAttr(oidcTrustConfigDataSourceAddress, "name", "organizations/a1b2c3/serviceAccounts/ci-agent/oidcTrustConfigs/github-actions"),
				tfresource.TestCheckResourceAttr(oidcTrustConfigDataSourceAddress, "uid", "74931f1b-b5cc-48d4-9a97-24073d43fd82"),
				tfresource.TestCheckResourceAttr(oidcTrustConfigDataSourceAddress, "description", "GitHub Actions deployments"),
				tfresource.TestCheckResourceAttr(oidcTrustConfigDataSourceAddress, "cel", `token.repository == "coreweave/example"`),
				tfresource.TestCheckResourceAttr(oidcTrustConfigDataSourceAddress, "workload_federation_oidc_config_id", "13db6848-17e8-42b0-8615-4d3fc86bd721"),
				tfresource.TestCheckResourceAttr(oidcTrustConfigDataSourceAddress, "created_at", createdAt.Format(time.RFC3339)),
				tfresource.TestCheckResourceAttr(oidcTrustConfigDataSourceAddress, "updated_at", updatedAt.Format(time.RFC3339)),
			),
		}},
	})
}

func TestOIDCTrustConfigDataSourceDiagnostics(t *testing.T) {
	tests := map[string]struct {
		organizationID   string
		serviceAccountID string
		id               string
		config           *directoryv1alpha.ServiceAccountOidcTrustConfig
		wantError        string
	}{
		"invalid organization ID": {
			organizationID:   "INVALID",
			serviceAccountID: "ci-agent",
			id:               "github-actions",
			wantError:        "Invalid Attribute Value Match",
		},
		"invalid service account ID": {
			organizationID:   "a1b2c3",
			serviceAccountID: "CI_AGENT",
			id:               "github-actions",
			wantError:        "Invalid Attribute Value Match",
		},
		"invalid trust config ID": {
			organizationID:   "a1b2c3",
			serviceAccountID: "ci-agent",
			id:               "GITHUB_ACTIONS",
			wantError:        "Invalid Attribute Value Match",
		},
		"not found": {
			organizationID:   "a1b2c3",
			serviceAccountID: "ci-agent",
			id:               "missing",
			wantError:        "OIDC Trust Configuration Not Found",
		},
		"invalid API response": {
			organizationID:   "a1b2c3",
			serviceAccountID: "ci-agent",
			id:               "github-actions",
			config: &directoryv1alpha.ServiceAccountOidcTrustConfig{
				Name: "organizations/a1b2c3/serviceAccounts/ci-agent/oidcTrustConfigs/github-actions",
			},
			wantError: "OIDC trust configuration UID is missing",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			service, server := startOIDCTrustConfigTestServer(t)
			if test.config != nil {
				seedOIDCTrustConfig(service, test.config)
			}
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				Steps: []tfresource.TestStep{{
					Config:      oidcTrustConfigDataSourceTestConfig(server.URL, test.organizationID, test.serviceAccountID, test.id),
					ExpectError: regexp.MustCompile(test.wantError),
				}},
			})
		})
	}
}

package workloadfederation_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	controlplanev1beta1 "buf.build/gen/go/coreweave/workload-federation/protocolbuffers/go/coreweave/workload_federation/control_plane/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	oidcConfigDataSourceAddress = "data.coreweave_workload_federation_oidc_config.test"
	testOIDCConfigUID           = "13db6848-17e8-42b0-8615-4d3fc86bd721"
	testOIDCConfigOtherUID      = "43f94626-afb8-4f05-9a2d-8ba29aee5923"
)

func oidcConfigDataSourceTestConfig(endpoint, selectors string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

data "coreweave_workload_federation_oidc_config" "test" {
%s
}
`, endpoint, selectors)
}

func testOIDCConfigs() (*controlplanev1beta1.OIDCConfig, *controlplanev1beta1.OIDCConfig) {
	now := time.Date(2026, time.August, 31, 12, 34, 56, 0, time.UTC)
	active := &controlplanev1beta1.OIDCConfig{
		Uid:       testOIDCConfigUID,
		OrgUid:    "org-test",
		Name:      "github-actions",
		IssuerUrl: "https://token.actions.githubusercontent.com",
		Audience:  "coreweave",
		CreatedAt: timestamppb.New(now),
		UpdatedAt: timestamppb.New(now),
	}
	deactivated := &controlplanev1beta1.OIDCConfig{
		Uid:           testOIDCConfigOtherUID,
		OrgUid:        "org-test",
		Name:          "deactivated-config",
		IssuerUrl:     "https://deactivated.example.com",
		Audience:      "coreweave",
		DeactivatedAt: timestamppb.New(now),
		CreatedAt:     timestamppb.New(now),
		UpdatedAt:     timestamppb.New(now),
	}
	return active, deactivated
}

func TestOIDCConfigDataSourceLookup(t *testing.T) {
	active, deactivated := testOIDCConfigs()

	tests := map[string]struct {
		selectors       string
		configs         []*controlplanev1beta1.OIDCConfig
		wantUID         string
		wantName        string
		wantActive      string
		wantDeactivated bool
	}{
		"ID lookup": {
			selectors:  fmt.Sprintf("  id = %q", testOIDCConfigUID),
			configs:    []*controlplanev1beta1.OIDCConfig{active},
			wantUID:    testOIDCConfigUID,
			wantName:   "github-actions",
			wantActive: "true",
		},
		"issuer and audience lookup": {
			selectors:  "  issuer_url = \"https://token.actions.githubusercontent.com\"\n  audience   = \"coreweave\"",
			configs:    []*controlplanev1beta1.OIDCConfig{deactivated, active},
			wantUID:    testOIDCConfigUID,
			wantName:   "github-actions",
			wantActive: "true",
		},
		"deactivated object": {
			selectors:       fmt.Sprintf("  id = %q", testOIDCConfigOtherUID),
			configs:         []*controlplanev1beta1.OIDCConfig{deactivated},
			wantUID:         testOIDCConfigOtherUID,
			wantName:        "deactivated-config",
			wantActive:      "false",
			wantDeactivated: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			service, server := startOIDCConfigTestServer(t)
			service.seed(test.configs...)

			checks := []tfresource.TestCheckFunc{
				tfresource.TestCheckResourceAttr(oidcConfigDataSourceAddress, "id", test.wantUID),
				tfresource.TestCheckResourceAttr(oidcConfigDataSourceAddress, "name", test.wantName),
				tfresource.TestCheckResourceAttr(oidcConfigDataSourceAddress, "active", test.wantActive),
			}
			if test.wantDeactivated {
				checks = append(checks, tfresource.TestCheckResourceAttrSet(oidcConfigDataSourceAddress, "deactivated_at"))
			} else {
				checks = append(checks, tfresource.TestCheckNoResourceAttr(oidcConfigDataSourceAddress, "deactivated_at"))
			}

			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				Steps: []tfresource.TestStep{{
					Config: oidcConfigDataSourceTestConfig(server.URL, test.selectors),
					Check:  tfresource.ComposeAggregateTestCheckFunc(checks...),
				}},
			})
		})
	}
}

func TestOIDCConfigDataSourceDiagnostics(t *testing.T) {
	active, _ := testOIDCConfigs()
	duplicate := proto.Clone(active).(*controlplanev1beta1.OIDCConfig)
	duplicate.Uid = testOIDCConfigOtherUID
	duplicate.Name = "same-trust-identity"

	tests := map[string]struct {
		selectors string
		configs   []*controlplanev1beta1.OIDCConfig
		wantError string
	}{
		"neither selector": {
			wantError: "Invalid OIDC Configuration Selector",
		},
		"both selectors": {
			selectors: fmt.Sprintf("  id = %q\n  issuer_url = %q\n  audience = %q", testOIDCConfigUID, active.GetIssuerUrl(), active.GetAudience()),
			configs:   []*controlplanev1beta1.OIDCConfig{active},
			wantError: "Invalid OIDC Configuration Selector",
		},
		"empty ID": {
			selectors: `  id = ""`,
			wantError: "Invalid Attribute Value Length",
		},
		"empty audience": {
			selectors: "  issuer_url = \"https://token.actions.githubusercontent.com\"\n  audience = \"\"",
			wantError: "Invalid Attribute Value Length",
		},
		"invalid issuer URL": {
			selectors: "  issuer_url = \"not-a-url\"\n  audience = \"coreweave\"",
			wantError: "Invalid OIDC issuer URL",
		},
		"issuer URL too long": {
			selectors: fmt.Sprintf("  issuer_url = %q\n  audience = \"coreweave\"", "https://example.com/"+strings.Repeat("a", 1024)),
			wantError: "Invalid Attribute Value Length",
		},
		"issuer without audience": {
			selectors: `  issuer_url = "https://token.actions.githubusercontent.com"`,
			wantError: "Invalid OIDC Configuration Selector",
		},
		"audience without issuer": {
			selectors: `  audience = "coreweave"`,
			wantError: "Invalid OIDC Configuration Selector",
		},
		"zero ID matches": {
			selectors: fmt.Sprintf("  id = %q", testOIDCConfigUID),
			wantError: `No workload federation OIDC configuration has the ID`,
		},
		"zero issuer and audience matches": {
			selectors: "  issuer_url = \"https://missing.example.com\"\n  audience = \"coreweave\"",
			configs:   []*controlplanev1beta1.OIDCConfig{active},
			wantError: `(?s)no workload federation OIDC configuration.*issuer URL`,
		},
		"multiple issuer and audience matches": {
			selectors: "  issuer_url = \"https://token.actions.githubusercontent.com\"\n  audience = \"coreweave\"",
			configs:   []*controlplanev1beta1.OIDCConfig{active, duplicate},
			wantError: `(?s)more than one workload federation OIDC configuration.*issuer URL`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			service, server := startOIDCConfigTestServer(t)
			service.seed(test.configs...)

			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				Steps: []tfresource.TestStep{{
					Config:      oidcConfigDataSourceTestConfig(server.URL, test.selectors),
					ExpectError: regexp.MustCompile(test.wantError),
				}},
			})
		})
	}
}

package serviceaccount

import (
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	resourceIDPattern     = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	organizationIDPattern = regexp.MustCompile(`^[a-z0-9]{6}$`)
	oidcTrustConfigNameRE = regexp.MustCompile(`^organizations/([a-z0-9]{6})/serviceAccounts/([a-z](?:[a-z0-9-]{0,61}[a-z0-9])?)/oidcTrustConfigs/([a-z](?:[a-z0-9-]{0,61}[a-z0-9])?)$`)
	uuidPattern           = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
)

func serviceAccountName(organizationID, serviceAccountID string) string {
	return fmt.Sprintf("organizations/%s/serviceAccounts/%s", organizationID, serviceAccountID)
}

func oidcTrustConfigName(organizationID, serviceAccountID, configID string) string {
	return fmt.Sprintf("%s/oidcTrustConfigs/%s", serviceAccountName(organizationID, serviceAccountID), configID)
}

func parseOIDCTrustConfigName(name string) (string, string, string, error) {
	matches := oidcTrustConfigNameRE.FindStringSubmatch(name)
	if matches == nil {
		return "", "", "", fmt.Errorf("expected organizations/{organization}/serviceAccounts/{service_account}/oidcTrustConfigs/{oidc_trust_config}, got %q", name)
	}
	return matches[1], matches[2], matches[3], nil
}

func timestampValue(timestamp *timestamppb.Timestamp) types.String {
	if timestamp == nil {
		return types.StringNull()
	}
	return types.StringValue(timestamp.AsTime().Format(time.RFC3339))
}

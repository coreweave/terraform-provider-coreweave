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
	serviceAccountNameRE  = regexp.MustCompile(`^organizations/([a-z0-9]{6})/serviceAccounts/([a-z](?:[a-z0-9-]{0,61}[a-z0-9])?)$`)
)

func parseServiceAccountName(name string) (string, string, error) {
	matches := serviceAccountNameRE.FindStringSubmatch(name)
	if matches == nil {
		return "", "", fmt.Errorf("expected organizations/{organization}/serviceAccounts/{service_account}, got %q", name)
	}
	return matches[1], matches[2], nil
}

func timestampValue(timestamp *timestamppb.Timestamp) types.String {
	if timestamp == nil {
		return types.StringNull()
	}
	return types.StringValue(timestamp.AsTime().Format(time.RFC3339))
}

package serviceaccount

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	createReadBackTimeout      = 30 * time.Second
	createReadBackInitialDelay = 250 * time.Millisecond
	createReadBackMaxDelay     = 5 * time.Second
)

// createReadBackPolicy bounds how long a resource waits for an object it just
// created to become readable.
type createReadBackPolicy struct {
	timeout      time.Duration
	initialDelay time.Duration
	maxDelay     time.Duration
}

func defaultCreateReadBackPolicy() createReadBackPolicy {
	return createReadBackPolicy{
		timeout:      createReadBackTimeout,
		initialDelay: createReadBackInitialDelay,
		maxDelay:     createReadBackMaxDelay,
	}
}

const iamPrivateBetaBanner = "> **Private beta:** This resource is available only to organizations enrolled in the private beta and may change before general availability. It is gated by an organization-level feature flag. Contact your CoreWeave representative or CoreWeave Support to request access for your organization."

var (
	resourceIDPattern     = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	organizationIDPattern = regexp.MustCompile(`^[a-z0-9]{6}$`)
	accessPolicyNameRE    = regexp.MustCompile(`^organizations/([a-z0-9]{6})/accessPolicies/([a-z](?:[a-z0-9-]{0,61}[a-z0-9])?)$`)
	uuidV4Pattern         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	oidcTrustConfigNameRE = regexp.MustCompile(`^organizations/([a-z0-9]{6})/serviceAccounts/([a-z](?:[a-z0-9-]{0,61}[a-z0-9])?)/oidcTrustConfigs/([a-z](?:[a-z0-9-]{0,61}[a-z0-9])?)$`)
	serviceAccountNameRE  = regexp.MustCompile(`^organizations/([a-z0-9]{6})/serviceAccounts/([a-z](?:[a-z0-9-]{0,61}[a-z0-9])?)$`)
	uuidPattern           = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
)

func serviceAccountName(organizationID, serviceAccountID string) string {
	return fmt.Sprintf("organizations/%s/serviceAccounts/%s", organizationID, serviceAccountID)
}

func parseAccessPolicyName(name string) (string, string, error) {
	matches := accessPolicyNameRE.FindStringSubmatch(name)
	if matches == nil {
		return "", "", fmt.Errorf("expected organizations/{organization}/accessPolicies/{access_policy_id}, got %q", name)
	}
	return matches[1], matches[2], nil
}

func parseOIDCTrustConfigName(name string) (string, string, string, error) {
	matches := oidcTrustConfigNameRE.FindStringSubmatch(name)
	if matches == nil {
		return "", "", "", fmt.Errorf("expected organizations/{organization}/serviceAccounts/{service_account}/oidcTrustConfigs/{oidc_trust_config}, got %q", name)
	}
	return matches[1], matches[2], matches[3], nil
}

func parseServiceAccountName(name string) (string, string, error) {
	matches := serviceAccountNameRE.FindStringSubmatch(name)
	if matches == nil {
		return "", "", fmt.Errorf("expected organizations/{organization}/serviceAccounts/{service_account}, got %q", name)
	}
	return matches[1], matches[2], nil
}

// readBackCreated retries only NotFound because a successful create may become
// visible to reads shortly after the write completes. The shared client owns
// transport retries, so other codes, including Unavailable, are returned
// immediately. kind names the resource in errors, for example "access policy".
func readBackCreated[T any](
	ctx context.Context,
	policy createReadBackPolicy,
	kind string,
	get func(context.Context) (T, error),
) (T, error) {
	var zero T
	retryCtx, cancel := context.WithTimeout(ctx, policy.timeout)
	defer cancel()

	delay := policy.initialDelay
	for {
		value, err := get(retryCtx)
		if err == nil {
			return value, nil
		}
		if retryCtx.Err() != nil {
			return zero, readBackError(policy, kind, err, ctx.Err(), retryCtx.Err())
		}
		if !coreweave.IsNotFoundError(err) {
			return zero, err
		}

		timer := time.NewTimer(delay)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return zero, readBackError(policy, kind, err, ctx.Err(), retryCtx.Err())
		case <-timer.C:
		}

		if delay < policy.maxDelay {
			delay *= 2
			if delay > policy.maxDelay {
				delay = policy.maxDelay
			}
		}
	}
}

func readBackError(policy createReadBackPolicy, kind string, lastErr, parentErr, retryErr error) error {
	if parentErr != nil {
		return fmt.Errorf("%s read-back was canceled (last error: %w): %w", kind, lastErr, parentErr)
	}
	return fmt.Errorf("%s could not be read back within %s (last error: %w): %w", kind, policy.timeout, lastErr, retryErr)
}

func timestampValue(timestamp *timestamppb.Timestamp) types.String {
	if timestamp == nil {
		return types.StringNull()
	}
	return types.StringValue(timestamp.AsTime().Format(time.RFC3339))
}

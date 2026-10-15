package containerregistry_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	containerregistry "github.com/coreweave/terraform-provider-coreweave/coreweave/container_registry"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// bootstrapConfig declares access atomically with namespace creation.
func bootstrapConfig(name, zone string, allow, force bool) string {
	bootstrap := "{}"
	if allow {
		bootstrap = `{ policy_sets = { principal = { identity_selector = "COREWEAVE", rules = { content = { expression = "true" } } } } }`
	}
	return strings.Replace(namespaceConfig(name, zone, "null"), " force_destroy = false", fmt.Sprintf(" force_destroy = %t\n initial_access_configuration = %s", force, bootstrap), 1)
}

// TestAccNamespaceBootstrap checks empty deny-all and configured access in the create RPC.
func TestAccNamespaceBootstrap(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(fmt.Sprintf("allow_%t", allow), func(t *testing.T) {
			name, zone := acceptanceNamespace(t)
			config := bootstrapConfig(name, zone, allow, false)
			resource.ParallelTest(t, resource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				CheckDestroy:             checkNamespaceDestroyed(t, name),
				Steps: []resource.TestStep{{Config: config, ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue("coreweave_container_registry_namespace.test", tfjsonpath.New("initial_access_configuration"), knownvalue.NotNull())}, Check: func(_ *terraform.State) error {
					client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{}, "", "")
					if err != nil {
						return err
					}
					response, err := client.ContainerRegistry.GetRegistryAccessConfiguration(t.Context(), &api.GetRegistryAccessConfigurationRequest{Parent: "namespaces/" + name})
					if err != nil {
						return err
					}
					policy := response
					if !allow {
						if len(policy.PolicySets) != 0 || policy.RequestIpAcl != nil {
							return fmt.Errorf("empty bootstrap did not install deny-all")
						}
						return nil
					}
					if len(policy.PolicySets) != 1 || policy.PolicySets[0].Id != "principal" || len(policy.PolicySets[0].Rules) != 1 || policy.PolicySets[0].Rules[0].Expression != "true" {
						return fmt.Errorf("atomic bootstrap policy does not match configuration")
					}
					return nil
				}}, {ResourceName: "coreweave_container_registry_namespace.test", ImportState: true, ImportStateId: name, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"initial_access_configuration", "content_status", "access_mode", "updated_at", "updated_by"}}},
			})
		})
	}
}

var namespaceNotEmptyError = regexp.MustCompile(`(?i)(NAMESPACE_NOT_EMPTY|namespace[^\n]*is not empty)`)

// TestAccNamespaceNonemptyDestroy checks refusal, then an explicitly applied force setting.
func TestAccNamespaceNonemptyDestroy(t *testing.T) {
	name, zone := acceptanceNamespace(t)
	registerNonemptyNamespaceCleanup(t, name, zone)
	config := bootstrapConfig(name, zone, true, false)
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             checkNamespaceDestroyed(t, name),
		Steps: []resource.TestStep{
			{Config: config, Check: func(_ *terraform.State) error { return pushAcceptanceImage(t, name) }},
			{Config: config, Destroy: true, ExpectError: namespaceNotEmptyError},
			{Config: bootstrapConfig(name, zone, true, true), ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue("coreweave_container_registry_namespace.test", tfjsonpath.New("force_destroy"), knownvalue.Bool(true))}},
		},
	})
}

// registerNonemptyNamespaceCleanup removes this test's namespace if an assertion prevents the force-setting step.
// Terraform's CheckDestroy still runs first and reports any provider cleanup failure.
func registerNonemptyNamespaceCleanup(t *testing.T, name, zone string) {
	t.Helper()
	prefix, err := registryTestPrefix()
	require.NoError(t, err)
	require.True(t, sweepTarget(&api.RegistryNamespace{Name: "namespaces/" + name, Zone: zone}, prefix, zone))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 20*time.Minute)
		defer cancel()
		configured, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
		if err != nil {
			t.Errorf("building namespace cleanup client: %s", err)
			return
		}
		registry := configured.ContainerRegistry
		namespace, err := registry.GetRegistryNamespace(ctx, &api.GetRegistryNamespaceRequest{Name: "namespaces/" + name})
		if coreweave.IsNotFoundError(err) {
			return
		}
		if err != nil {
			t.Errorf("reading test namespace for cleanup: %s", err)
			return
		}
		if namespace.Name != "namespaces/"+name || !sweepTarget(namespace, prefix, zone) {
			t.Errorf("refusing cleanup of namespace outside this test's name and zone")
			return
		}
		key, err := containerregistry.NewIdempotencyKey()
		if err != nil {
			t.Errorf("generating namespace cleanup key: %s", err)
			return
		}
		operation, err := registry.DeleteRegistryNamespace(ctx, &api.DeleteRegistryNamespaceRequest{Name: namespace.Name, Force: true, IdempotencyKey: key})
		if coreweave.IsNotFoundError(err) {
			return
		}
		if err == nil {
			err = containerregistry.WaitOperation(ctx, registry, operation, &emptypb.Empty{})
		}
		if err != nil {
			t.Errorf("cleaning up nonempty test namespace: %s", err)
		}
	})
}

// TestNamespaceNotEmptyError matches the public API diagnostic without accepting unrelated failures.
func TestNamespaceNotEmptyError(t *testing.T) {
	for _, message := range []string{
		`Error: Failed Precondition
NAMESPACE_NOT_EMPTY: namespace "namespaces/q-tfacc-cr-91058df7" is not empty`,
		`namespace "namespaces/q-tfacc-cr-91058df7" is not empty`,
	} {
		require.Regexp(t, namespaceNotEmptyError, message)
	}
	require.NotRegexp(t, namespaceNotEmptyError, "Error: Permission Denied")
	require.NotRegexp(t, namespaceNotEmptyError, "failed to read content status")
}

// TestNonemptyNamespaceCleanup runs after early exit with a canceled test context and tolerates prior deletion.
func TestNonemptyNamespaceCleanup(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(fmt.Sprintf("absent_%t", absent), func(t *testing.T) {
			h := newHarness(t, "namespace")
			t.Setenv("COREWEAVE_CONTAINER_REGISTRY_TEST_PREFIX", "q-tfacc-cr-")
			if absent {
				h.fake.namespace = nil
			} else {
				h.fake.namespace.Name = "namespaces/q-tfacc-cr-0123abcd"
			}
			t.Run("early_exit", func(t *testing.T) {
				registerNonemptyNamespaceCleanup(t, "q-tfacc-cr-0123abcd", "US-LAB-01A")
				t.Skip("exercise cleanup after an early test exit")
			})
			if absent {
				require.Empty(t, h.fake.deletes)
			} else {
				require.Len(t, h.fake.deletes, 1)
				require.True(t, h.fake.deletes[0].Force)
				require.Nil(t, h.fake.namespace)
			}
		})
	}
}

// registryRequest exchanges the QA principal credential for a scoped registry token when challenged.
func registryRequest(ctx context.Context, client *http.Client, method, target, media string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if media != "" {
		request.Header.Set("Content-Type", media)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusUnauthorized {
		return response, nil
	}
	challenge := response.Header.Get("WWW-Authenticate")
	response.Body.Close()
	// The registry's RFC 6750 challenge contains quoted realm/service fields.
	fields := regexp.MustCompile(`([a-z]+)="([^"]*)"`).FindAllStringSubmatch(challenge, -1)
	values := map[string]string{}
	for _, field := range fields {
		values[field[1]] = field[2]
	}
	realm, err := url.Parse(values["realm"])
	if err != nil {
		return nil, err
	}
	destination := response.Request.URL
	if realm.Scheme != "https" || realm.Host != destination.Host {
		return nil, fmt.Errorf("unexpected registry authentication realm")
	}
	query := realm.Query()
	query.Set("service", values["service"])
	query.Set("scope", "repository:tfacc/safety:pull,push")
	realm.RawQuery = query.Encode()
	auth, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return nil, err
	}
	auth.SetBasicAuth("principal", os.Getenv("COREWEAVE_API_TOKEN"))
	tokenResponse, err := client.Do(auth)
	if err != nil {
		return nil, err
	}
	defer tokenResponse.Body.Close()
	if tokenResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry token exchange returned HTTP %d", tokenResponse.StatusCode)
	}
	var token struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err = json.NewDecoder(tokenResponse.Body).Decode(&token); err != nil {
		return nil, err
	}
	if token.Token == "" {
		token.Token = token.AccessToken
	}
	if token.Token == "" {
		return nil, fmt.Errorf("registry token exchange returned no token")
	}
	request, err = http.NewRequestWithContext(ctx, method, destination.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token.Token)
	if media != "" {
		request.Header.Set("Content-Type", media)
	}
	return client.Do(request)
}

// pushAcceptanceImage uploads a tiny OCI config and manifest without an external CLI dependency.
func pushAcceptanceImage(t *testing.T, name string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	configured, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
	if err != nil {
		return err
	}
	err = coreweave.PollUntil("bootstrap policy delivery", ctx, 3*time.Second, 5*time.Minute, func(ctx context.Context) (bool, error) {
		response, err := configured.ContainerRegistry.GetRegistryAccessConfiguration(ctx, &api.GetRegistryAccessConfigurationRequest{Parent: "namespaces/" + name})
		if err != nil {
			return false, err
		}
		return response.AccessConfigState == api.RegistryAccessConfiguration_ACCESS_CONFIG_STATE_ACCEPTED, nil
	})
	if err != nil {
		return err
	}
	namespace, err := configured.ContainerRegistry.GetRegistryNamespace(ctx, &api.GetRegistryNamespaceRequest{Name: "namespaces/" + name})
	if err != nil {
		return err
	}
	base := "https://" + namespace.DnsName
	client := &http.Client{Timeout: 30 * time.Second}
	if err := waitRegistryEndpoint(ctx, client, base); err != nil {
		return err
	}
	config := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{}}`)
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(config))
	response, err := registryRequest(ctx, client, http.MethodPost, base+"/v2/tfacc/safety/blobs/uploads/", "", nil)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("start OCI upload returned HTTP %d", response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		return err
	}
	location = response.Request.URL.ResolveReference(location)
	// Upload locations must remain on this namespace's registry hosts.
	if location.Scheme != "https" || !strings.HasSuffix(location.Host, ".cwcr.io") || !strings.HasPrefix(location.Host, name+".") {
		return fmt.Errorf("unexpected OCI upload location")
	}
	query := location.Query()
	query.Set("digest", digest)
	location.RawQuery = query.Encode()
	response, err = registryRequest(ctx, client, http.MethodPut, location.String(), "application/octet-stream", config)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("complete OCI upload returned HTTP %d", response.StatusCode)
	}
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","size":%d,"digest":%q},"layers":[]}`, len(config), digest))
	response, err = registryRequest(ctx, client, http.MethodPut, base+"/v2/tfacc/safety/manifests/test", "application/vnd.oci.image.manifest.v1+json", manifest)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("publish OCI manifest returned HTTP %d", response.StatusCode)
	}
	return nil
}

// waitRegistryEndpoint allows namespace DNS and TLS routing to propagate without bypassing verification.
func waitRegistryEndpoint(ctx context.Context, client *http.Client, base string) error {
	return coreweave.PollUntil("namespace DNS and TLS readiness", ctx, 3*time.Second, 5*time.Minute, func(ctx context.Context) (bool, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v2/", nil)
		if err != nil {
			return false, err
		}
		response, err := client.Do(request)
		if err != nil {
			var hostname x509.HostnameError
			var dns *net.DNSError
			if errors.As(err, &hostname) || (errors.As(err, &dns) && (dns.IsNotFound || dns.IsTemporary)) {
				return false, nil
			}
			return false, err
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return response.StatusCode == http.StatusOK || response.StatusCode == http.StatusUnauthorized, nil
	})
}

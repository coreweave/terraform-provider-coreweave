package coreweave

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cwobjectv1connect "buf.build/gen/go/coreweave/cwobject/connectrpc/go/cwobject/v1/cwobjectv1connect"
	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testS3AccessKey = "test-access-key"
	testS3SecretKey = "test-secret-key"
)

type s3CWObjectClientStub struct {
	cwobjectv1connect.CWObjectClient
	t *testing.T
}

func isolateAWSEnvironment(t *testing.T) {
	t.Helper()

	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(name, "AWS_") {
			t.Setenv(name, "")
		}
	}
}

func testCACertificatePEM(t *testing.T) []byte {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test CA key: %v", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "CoreWeave S3 configuration test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatalf("create test CA certificate: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func (s *s3CWObjectClientStub) CreateAccessKeyFromJWT(
	_ context.Context,
	req *connect.Request[cwobjectv1.CreateAccessKeyFromJWTRequest],
) (*connect.Response[cwobjectv1.CreateAccessKeyFromJWTResponse], error) {
	s.t.Helper()

	if got, want := req.Msg.GetDurationSeconds().GetValue(), uint32(900); got != want {
		s.t.Fatalf("access key lifetime = %d seconds, want %d", got, want)
	}

	return connect.NewResponse(&cwobjectv1.CreateAccessKeyFromJWTResponse{
		AccessKeyId: testS3AccessKey,
		SecretKey:   testS3SecretKey,
	}), nil
}

func TestCreateS3Client_DefaultAWSConfiguration(t *testing.T) {
	isolateAWSEnvironment(t)

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config")
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		t.Fatalf("write isolated config file: %v", err)
	}
	credentialsPath := filepath.Join(tempDir, "credentials")
	if err := os.WriteFile(credentialsPath, nil, 0o600); err != nil {
		t.Fatalf("write isolated credentials file: %v", err)
	}

	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_DEFAULTS_MODE", "standard")

	client := &Client{
		CWObjectClient: &s3CWObjectClientStub{t: t},
		s3Endpoint:     "https://objects.example.test",
	}

	s3Client, keyInfo, err := client.createS3Client(t.Context(), "US-TEST-01A")
	if err != nil {
		t.Fatalf("create S3 client: %v", err)
	}
	if s3Client == nil {
		t.Fatal("create S3 client returned nil client")
	}
	if keyInfo.GetAccessKeyId() != testS3AccessKey || keyInfo.GetSecretKey() != testS3SecretKey {
		t.Fatalf("issued credentials = (%q, %q), want (%q, %q)",
			keyInfo.GetAccessKeyId(), keyInfo.GetSecretKey(), testS3AccessKey, testS3SecretKey)
	}
}

func TestCreateS3Client_IgnoresAWSConfiguration(t *testing.T) {
	const (
		endpoint = "https://objects.example.test"
		zone     = "US-TEST-01A"
		profile  = "coreweave-s3-test"
	)

	tempDir := t.TempDir()
	caBundlePath := filepath.Join(tempDir, "ca-bundle.pem")
	if err := os.WriteFile(caBundlePath, testCACertificatePEM(t), 0o600); err != nil {
		t.Fatalf("write CA bundle: %v", err)
	}

	credentialsPath := filepath.Join(tempDir, "credentials")
	if err := os.WriteFile(credentialsPath, nil, 0o600); err != nil {
		t.Fatalf("write isolated credentials file: %v", err)
	}

	tests := map[string]func(t *testing.T, configPath string){
		"AWS_CA_BUNDLE": func(t *testing.T, configPath string) {
			t.Helper()
			config := fmt.Sprintf("[profile %s]\n", profile)
			if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
				t.Fatalf("write isolated config file: %v", err)
			}
			t.Setenv("AWS_CA_BUNDLE", caBundlePath)
		},
		"selected shared profile ca_bundle": func(t *testing.T, configPath string) {
			t.Helper()
			config := fmt.Sprintf("[profile %s]\nca_bundle = %s\n", profile, caBundlePath)
			if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
				t.Fatalf("write isolated config file: %v", err)
			}
			t.Setenv("AWS_CA_BUNDLE", "")
		},
		"malformed environment configuration": func(t *testing.T, configPath string) {
			t.Helper()
			config := fmt.Sprintf("[profile %s]\n", profile)
			if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
				t.Fatalf("write isolated config file: %v", err)
			}
			t.Setenv("AWS_MAX_ATTEMPTS", "invalid")
		},
		"selected shared profile configuration": func(t *testing.T, configPath string) {
			t.Helper()
			config := fmt.Sprintf(`[profile %s]
region = us-east-1
retry_mode = adaptive
max_attempts = 2
use_fips_endpoint = true
use_dualstack_endpoint = true
endpoint_url = https://wrong.example.test
`, profile)
			if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
				t.Fatalf("write isolated config file: %v", err)
			}
		},
	}

	for _, name := range []string{
		"AWS_CA_BUNDLE",
		"selected shared profile ca_bundle",
		"malformed environment configuration",
		"selected shared profile configuration",
	} {
		t.Run(name, func(t *testing.T) {
			isolateAWSEnvironment(t)

			configPath := filepath.Join(tempDir, name+".config")
			tests[name](t, configPath)

			t.Setenv("AWS_CONFIG_FILE", configPath)
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
			t.Setenv("AWS_PROFILE", profile)
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			t.Setenv("AWS_DEFAULTS_MODE", "standard")

			client := &Client{
				CWObjectClient: &s3CWObjectClientStub{t: t},
				s3Endpoint:     endpoint,
			}

			s3Client, keyInfo, err := client.createS3Client(t.Context(), zone)
			if err != nil {
				t.Fatalf("create S3 client: %v", err)
			}
			if s3Client == nil {
				t.Fatal("create S3 client returned nil client")
			}
			if keyInfo.GetAccessKeyId() != testS3AccessKey || keyInfo.GetSecretKey() != testS3SecretKey {
				t.Fatalf("issued credentials = (%q, %q), want (%q, %q)",
					keyInfo.GetAccessKeyId(), keyInfo.GetSecretKey(), testS3AccessKey, testS3SecretKey)
			}

			options := s3Client.Options()
			if options.BaseEndpoint == nil || *options.BaseEndpoint != endpoint {
				t.Fatalf("base endpoint = %v, want %q", options.BaseEndpoint, endpoint)
			}
			if options.Region != zone {
				t.Errorf("region = %q, want %q", options.Region, zone)
			}
			if options.UsePathStyle {
				t.Error("path-style addressing enabled, want virtual-hosted addressing")
			}
			if options.RetryMode != aws.RetryModeStandard {
				t.Errorf("AWS SDK retry mode = %q, want %q", options.RetryMode, aws.RetryModeStandard)
			}
			if options.RetryMaxAttempts != 0 {
				t.Errorf("AWS SDK retry max attempts = %d, want 0", options.RetryMaxAttempts)
			}
			if options.EndpointOptions.UseFIPSEndpoint != aws.FIPSEndpointStateUnset {
				t.Errorf("FIPS endpoint state = %d, want unset", options.EndpointOptions.UseFIPSEndpoint)
			}
			if options.EndpointOptions.UseDualStackEndpoint != aws.DualStackEndpointStateUnset {
				t.Errorf("dual-stack endpoint state = %d, want unset",
					options.EndpointOptions.UseDualStackEndpoint)
			}
			if options.RequestChecksumCalculation != aws.RequestChecksumCalculationWhenSupported {
				t.Errorf("request checksum calculation = %d, want when supported",
					options.RequestChecksumCalculation)
			}
			if options.ResponseChecksumValidation != aws.ResponseChecksumValidationWhenSupported {
				t.Errorf("response checksum validation = %d, want when supported",
					options.ResponseChecksumValidation)
			}

			httpClient, ok := options.HTTPClient.(*http.Client)
			if !ok {
				t.Fatalf("HTTP client type = %T, want *http.Client", options.HTTPClient)
			}

			transport, ok := httpClient.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("HTTP transport type = %T, want *http.Transport", httpClient.Transport)
			}
			if httpClient.Timeout != DefaultS3AttemptTimeout {
				t.Errorf("HTTP client timeout = %s, want %s",
					httpClient.Timeout, DefaultS3AttemptTimeout)
			}
			if options.Retryer == nil {
				t.Fatal("S3 retryer is nil")
			}
			if got := options.Retryer.MaxAttempts(); got != s3MaxAttempts {
				t.Errorf("S3 max attempts = %d, want %d", got, s3MaxAttempts)
			}

			if !transport.DisableKeepAlives {
				t.Error("inner HTTP transport keep-alives enabled, want disabled")
			}
			if transport.MaxIdleConnsPerHost != -1 {
				t.Errorf("inner HTTP transport max idle connections per host = %d, want -1",
					transport.MaxIdleConnsPerHost)
			}

			credentials, err := options.Credentials.Retrieve(t.Context())
			if err != nil {
				t.Fatalf("retrieve credentials: %v", err)
			}
			if credentials.AccessKeyID != testS3AccessKey || credentials.SecretAccessKey != testS3SecretKey {
				t.Errorf("client credentials = (%q, %q), want (%q, %q)",
					credentials.AccessKeyID, credentials.SecretAccessKey, testS3AccessKey, testS3SecretKey)
			}
		})
	}
}

type zeroBackoff struct{}

func (zeroBackoff) BackoffDelay(int, error) (time.Duration, error) {
	return 0, nil
}

type countingRoundTripper struct {
	count atomic.Int32
}

func (r *countingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	r.count.Add(1)
	return &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Status:     "503 Service Unavailable",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("<Error><Code>ServiceUnavailable</Code></Error>")),
	}, nil
}

func TestCreateS3Client_UsesOneBoundedRetryLayer(t *testing.T) {
	isolateAWSEnvironment(t)

	transport := &countingRoundTripper{}
	client := &Client{
		CWObjectClient:   &s3CWObjectClientStub{t: t},
		s3Endpoint:       "https://objects.example.test",
		s3AttemptTimeout: 7 * time.Second,
		s3HTTPTransport:  transport,
		s3Retryer: func() aws.Retryer {
			return newS3Retryer(func(options *awsretry.StandardOptions) {
				options.Backoff = zeroBackoff{}
			})
		},
	}

	s3Client, _, err := client.createS3Client(t.Context(), "US-TEST-01A")
	if err != nil {
		t.Fatalf("create S3 client: %v", err)
	}

	options := s3Client.Options()
	httpClient, ok := options.HTTPClient.(*http.Client)
	if !ok {
		t.Fatalf("HTTP client type = %T, want *http.Client", options.HTTPClient)
	}
	if got := httpClient.Timeout; got != 7*time.Second {
		t.Fatalf("HTTP client timeout = %s, want 7s", got)
	}
	if httpClient.Transport != transport {
		t.Fatalf("HTTP transport = %T, want injected counting transport", httpClient.Transport)
	}

	_, err = s3Client.ListBuckets(t.Context(), &s3.ListBucketsInput{})
	if err == nil {
		t.Fatal("ListBuckets() error = nil, want exhausted retry error")
	}
	if got := transport.count.Load(); got != s3MaxAttempts {
		t.Fatalf("transport sends = %d, want exactly %d", got, s3MaxAttempts)
	}
}

type principalS3CWObjectClientStub struct {
	cwobjectv1connect.CWObjectClient
	accessKeyID string
	calls       atomic.Int32
}

func (s *principalS3CWObjectClientStub) CreateAccessKeyFromJWT(
	context.Context,
	*connect.Request[cwobjectv1.CreateAccessKeyFromJWTRequest],
) (*connect.Response[cwobjectv1.CreateAccessKeyFromJWTResponse], error) {
	s.calls.Add(1)
	return connect.NewResponse(&cwobjectv1.CreateAccessKeyFromJWTResponse{
		AccessKeyId: s.accessKeyID,
		SecretKey:   "secret-" + s.accessKeyID,
		Expiry:      timestamppb.New(time.Now().Add(15 * time.Minute)),
	}), nil
}

func newS3CacheTestClientWithSource(t *testing.T, source AccessTokenSource, service cwobjectv1connect.CWObjectClient) *Client {
	t.Helper()

	client, err := NewClient(
		"https://api.example.test",
		"https://objects.example.test",
		time.Second,
		source,
		"test-user-agent",
	)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	client.CWObjectClient = service
	client.s3HTTPTransport = successfulListBucketsRoundTripper{}
	return client
}

func newS3CacheTestClient(t *testing.T, token string, service cwobjectv1connect.CWObjectClient) *Client {
	t.Helper()
	return newS3CacheTestClientWithSource(t, auth.NewStaticTokenSource(token), service)
}

func s3AccessKeyID(t *testing.T, client *s3.Client) string {
	t.Helper()

	credentials, err := client.Options().Credentials.Retrieve(t.Context())
	if err != nil {
		t.Fatalf("retrieve S3 credentials: %v", err)
	}
	return credentials.AccessKeyID
}

func TestS3ClientCacheIsolatedAcrossAuthPrincipals(t *testing.T) {
	resetS3ClientCache()
	t.Cleanup(resetS3ClientCache)

	principalA := &principalS3CWObjectClientStub{accessKeyID: "principal-a-key"}
	clientA := newS3CacheTestClient(t, "principal-a-token", principalA)
	s3ClientA, err := clientA.S3Client(t.Context(), "US-TEST-01A")
	if err != nil {
		t.Fatalf("create principal A S3 client: %v", err)
	}

	principalB := &principalS3CWObjectClientStub{accessKeyID: "principal-b-key"}
	clientB := newS3CacheTestClient(t, "principal-b-token", principalB)
	s3ClientB, err := clientB.S3Client(t.Context(), "US-TEST-01A")
	if err != nil {
		t.Fatalf("create principal B S3 client: %v", err)
	}

	if s3ClientA == s3ClientB {
		t.Fatal("different auth principals reused the same cached S3 client")
	}
	if got := s3AccessKeyID(t, s3ClientA); got != "principal-a-key" {
		t.Fatalf("principal A access key = %q, want %q", got, "principal-a-key")
	}
	if got := s3AccessKeyID(t, s3ClientB); got != "principal-b-key" {
		t.Fatalf("principal B access key = %q, want %q", got, "principal-b-key")
	}
	if got := principalA.calls.Load(); got != 1 {
		t.Fatalf("principal A access-key requests = %d, want 1", got)
	}
	if got := principalB.calls.Load(); got != 1 {
		t.Fatalf("principal B access-key requests = %d, want 1", got)
	}
}

func TestS3ClientCacheReusedForSameStaticCredential(t *testing.T) {
	resetS3ClientCache()
	t.Cleanup(resetS3ClientCache)

	firstService := &principalS3CWObjectClientStub{accessKeyID: "shared-key"}
	firstClient := newS3CacheTestClient(t, "shared-token", firstService)
	firstS3Client, err := firstClient.S3Client(t.Context(), "US-TEST-01A")
	if err != nil {
		t.Fatalf("create first S3 client: %v", err)
	}

	secondService := &principalS3CWObjectClientStub{accessKeyID: "unexpected-key"}
	secondClient := newS3CacheTestClient(t, "shared-token", secondService)
	secondS3Client, err := secondClient.S3Client(t.Context(), "US-TEST-01A")
	if err != nil {
		t.Fatalf("get cached S3 client: %v", err)
	}

	if firstS3Client != secondS3Client {
		t.Fatal("clients with the same static credential did not share the cached S3 client")
	}
	if got := secondService.calls.Load(); got != 0 {
		t.Fatalf("second access-key service calls = %d, want 0", got)
	}
}

func newWorkloadIdentityS3CacheTestClient(t *testing.T, serviceAccountUID string, service cwobjectv1connect.CWObjectClient) *Client {
	t.Helper()

	t.Setenv(auth.TerraformCloudWorkloadIdentityTokenEnvVar, "e30.e30.c2ln")
	source, err := auth.NewWorkloadIdentityTokenSource(
		t.Context(),
		"https://api.example.test",
		serviceAccountUID,
		"test-user-agent",
		10*time.Second,
	)
	if err != nil {
		t.Fatalf("create workload identity token source: %v", err)
	}
	return newS3CacheTestClientWithSource(t, source, service)
}

func TestS3ClientCacheReusedForSameWorkloadIdentity(t *testing.T) {
	resetS3ClientCache()
	t.Cleanup(resetS3ClientCache)

	firstService := &principalS3CWObjectClientStub{accessKeyID: "shared-key"}
	firstClient := newWorkloadIdentityS3CacheTestClient(t, "service-account-one", firstService)
	firstS3Client, err := firstClient.S3Client(t.Context(), "US-TEST-01A")
	if err != nil {
		t.Fatalf("create first S3 client: %v", err)
	}

	secondService := &principalS3CWObjectClientStub{accessKeyID: "unexpected-key"}
	secondClient := newWorkloadIdentityS3CacheTestClient(t, "service-account-one", secondService)
	secondS3Client, err := secondClient.S3Client(t.Context(), "US-TEST-01A")
	if err != nil {
		t.Fatalf("get cached S3 client: %v", err)
	}

	if firstS3Client != secondS3Client {
		t.Fatal("clients for the same workload identity did not share the cached S3 client")
	}
	if got := secondService.calls.Load(); got != 0 {
		t.Fatalf("second access-key service calls = %d, want 0", got)
	}
}

func TestS3ClientCacheIsolatedAcrossWorkloadIdentities(t *testing.T) {
	resetS3ClientCache()
	t.Cleanup(resetS3ClientCache)

	principalA := &principalS3CWObjectClientStub{accessKeyID: "principal-a-key"}
	clientA := newWorkloadIdentityS3CacheTestClient(t, "service-account-one", principalA)
	s3ClientA, err := clientA.S3Client(t.Context(), "US-TEST-01A")
	if err != nil {
		t.Fatalf("create principal A S3 client: %v", err)
	}

	principalB := &principalS3CWObjectClientStub{accessKeyID: "principal-b-key"}
	clientB := newWorkloadIdentityS3CacheTestClient(t, "service-account-two", principalB)
	s3ClientB, err := clientB.S3Client(t.Context(), "US-TEST-01A")
	if err != nil {
		t.Fatalf("create principal B S3 client: %v", err)
	}

	if s3ClientA == s3ClientB {
		t.Fatal("different workload identities reused the same cached S3 client")
	}
	if got := s3AccessKeyID(t, s3ClientA); got != "principal-a-key" {
		t.Fatalf("principal A access key = %q, want %q", got, "principal-a-key")
	}
	if got := s3AccessKeyID(t, s3ClientB); got != "principal-b-key" {
		t.Fatalf("principal B access key = %q, want %q", got, "principal-b-key")
	}
	if got := principalA.calls.Load(); got != 1 {
		t.Fatalf("principal A access-key requests = %d, want 1", got)
	}
	if got := principalB.calls.Load(); got != 1 {
		t.Fatalf("principal B access-key requests = %d, want 1", got)
	}
}

type unkeyedTokenSource func(context.Context) (string, error)

func (s unkeyedTokenSource) Token(ctx context.Context) (string, error) {
	return s(ctx)
}

func TestTokenSourcesWithoutCacheIdentityAreIsolated(t *testing.T) {
	source := unkeyedTokenSource(func(context.Context) (string, error) { return "token", nil })
	first := tokenSourceCacheIdentity(source)
	second := tokenSourceCacheIdentity(source)

	if first == second {
		t.Fatal("token source without a stable cache identity was shared across clients")
	}
}

type blockingS3CWObjectClientStub struct {
	cwobjectv1connect.CWObjectClient
	started chan struct{}
	release chan struct{}
	err     error
	once    sync.Once
	calls   atomic.Int32
}

type cancelAwareS3CWObjectClientStub struct {
	cwobjectv1connect.CWObjectClient
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (s *cancelAwareS3CWObjectClientStub) CreateAccessKeyFromJWT(
	ctx context.Context,
	_ *connect.Request[cwobjectv1.CreateAccessKeyFromJWTRequest],
) (*connect.Response[cwobjectv1.CreateAccessKeyFromJWTResponse], error) {
	s.calls.Add(1)
	s.once.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
		return connect.NewResponse(&cwobjectv1.CreateAccessKeyFromJWTResponse{
			AccessKeyId: testS3AccessKey,
			SecretKey:   testS3SecretKey,
			Expiry:      timestamppb.New(time.Now().Add(15 * time.Minute)),
		}), nil
	}
}

type successfulListBucketsRoundTripper struct{}

func (successfulListBucketsRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			`<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Buckets></Buckets></ListAllMyBucketsResult>`,
		)),
		Request: request,
	}, nil
}

func (s *blockingS3CWObjectClientStub) CreateAccessKeyFromJWT(
	context.Context,
	*connect.Request[cwobjectv1.CreateAccessKeyFromJWTRequest],
) (*connect.Response[cwobjectv1.CreateAccessKeyFromJWTResponse], error) {
	s.calls.Add(1)
	s.once.Do(func() { close(s.started) })
	<-s.release
	if s.err == nil {
		s.err = errors.New("refresh failed")
	}
	return nil, s.err
}

func TestS3ClientWaitersShareRefreshFailure(t *testing.T) {
	resetS3ClientCache()
	t.Cleanup(resetS3ClientCache)

	refreshErr := errors.New("refresh failed")
	stub := &blockingS3CWObjectClientStub{
		started: make(chan struct{}),
		release: make(chan struct{}),
		err:     refreshErr,
	}
	client := &Client{
		CWObjectClient:  stub,
		apiEndpoint:     "https://api.example.test",
		s3Endpoint:      "https://objects.example.test",
		s3CacheIdentity: "test-token",
	}

	type result struct {
		client *s3.Client
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			got, err := client.S3Client(context.Background(), "US-TEST-01A")
			results <- result{client: got, err: err}
		}()
	}

	select {
	case <-stub.started:
	case <-time.After(time.Second):
		t.Fatal("credential refresh did not start")
	}

	select {
	case result := <-results:
		t.Fatalf("S3Client() returned before the in-flight refresh completed: client=%v error=%v", result.client, result.err)
	case <-time.After(25 * time.Millisecond):
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("CreateAccessKeyFromJWT calls before release = %d, want 1", got)
	}

	close(stub.release)
	for range 2 {
		select {
		case result := <-results:
			if result.client != nil {
				t.Fatal("S3Client() returned a client after failed initial refresh")
			}
			if !errors.Is(result.err, refreshErr) {
				t.Fatalf("S3Client() error = %v, want shared refresh error", result.err)
			}
		case <-time.After(time.Second):
			t.Fatal("S3Client() did not return after refresh failure")
		}
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("CreateAccessKeyFromJWT calls = %d, want 1", got)
	}
}

func TestS3ClientCanceledLeaderDoesNotFailHealthyWaiter(t *testing.T) {
	resetS3ClientCache()
	t.Cleanup(resetS3ClientCache)

	stub := &cancelAwareS3CWObjectClientStub{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	client := &Client{
		CWObjectClient:  stub,
		apiEndpoint:     "https://api.example.test",
		s3Endpoint:      "https://objects.example.test",
		s3HTTPTransport: successfulListBucketsRoundTripper{},
		s3CacheIdentity: "test-token",
	}
	type result struct {
		client *s3.Client
		err    error
	}

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leaderResult := make(chan result, 1)
	go func() {
		got, err := client.S3Client(leaderCtx, "US-TEST-01A")
		leaderResult <- result{client: got, err: err}
	}()
	select {
	case <-stub.started:
	case <-time.After(time.Second):
		t.Fatal("credential refresh did not start")
	}

	waiterResult := make(chan result, 1)
	go func() {
		got, err := client.S3Client(t.Context(), "US-TEST-01A")
		waiterResult <- result{client: got, err: err}
	}()
	select {
	case result := <-waiterResult:
		t.Fatalf("healthy waiter returned before refresh completed: client=%v error=%v", result.client, result.err)
	case <-time.After(25 * time.Millisecond):
	}

	cancelLeader()
	select {
	case result := <-leaderResult:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("canceled leader error = %v, want context canceled", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled leader did not return")
	}

	close(stub.release)
	select {
	case result := <-waiterResult:
		if result.err != nil || result.client == nil {
			t.Fatalf("healthy waiter result = (%v, %v), want client and nil error", result.client, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("healthy waiter did not receive completed refresh")
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("CreateAccessKeyFromJWT calls = %d, want one shared refresh", got)
	}
}

func TestS3ClientServesUnexpiredClientDuringRefresh(t *testing.T) {
	resetS3ClientCache()
	t.Cleanup(resetS3ClientCache)

	now := time.Now()
	cachedClient := s3.New(s3.Options{Region: "US-TEST-01A"})
	sharedS3ClientCache.mu.Lock()
	sharedS3ClientCache.client = cachedClient
	sharedS3ClientCache.accessKeyInfo = &cwobjectv1.CreateAccessKeyFromJWTResponse{
		Expiry: timestamppb.New(now.Add(time.Minute)),
	}
	sharedS3ClientCache.apiEndpoint = "https://api.example.test"
	sharedS3ClientCache.endpoint = "https://objects.example.test"
	sharedS3ClientCache.authIdentity = "test-token"
	sharedS3ClientCache.attemptTimeout = DefaultS3AttemptTimeout
	sharedS3ClientCache.mu.Unlock()

	stub := &blockingS3CWObjectClientStub{
		started: make(chan struct{}),
		release: make(chan struct{}),
		err:     errors.New("refresh failed"),
	}
	client := &Client{
		CWObjectClient:  stub,
		apiEndpoint:     "https://api.example.test",
		s3Endpoint:      "https://objects.example.test",
		s3CacheIdentity: "test-token",
		s3Now:           func() time.Time { return now },
	}

	type result struct {
		client *s3.Client
		err    error
	}
	refreshResult := make(chan result, 1)
	go func() {
		got, err := client.S3Client(context.Background(), "US-TEST-01A")
		refreshResult <- result{client: got, err: err}
	}()

	select {
	case <-stub.started:
	case <-time.After(time.Second):
		t.Fatal("credential refresh did not start")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	got, err := client.S3Client(ctx, "US-TEST-01A")
	if err != nil {
		t.Fatalf("concurrent S3Client() error = %v", err)
	}
	if got != cachedClient {
		t.Fatal("concurrent S3Client() did not return the unexpired cached client")
	}

	close(stub.release)
	select {
	case result := <-refreshResult:
		if result.err != nil {
			t.Fatalf("refreshing S3Client() error = %v", result.err)
		}
		if result.client != cachedClient {
			t.Fatal("failed refresh did not fall back to the unexpired cached client")
		}
	case <-time.After(time.Second):
		t.Fatal("credential refresh did not finish")
	}
}

func TestS3RetryerClassificationAndBackoffCap(t *testing.T) {
	t.Parallel()

	retryer := newS3Retryer()
	certificateErr := &url.Error{
		Op:  "Get",
		URL: "https://objects.example.test",
		Err: &tls.CertificateVerificationError{Err: errors.New("unknown authority")},
	}
	if retryer.IsErrorRetryable(certificateErr) {
		t.Fatal("certificate verification error is retryable")
	}
	if retryer.IsErrorRetryable(&smithy.GenericAPIError{Code: "AccessDenied"}) {
		t.Fatal("AccessDenied is retryable")
	}
	serverErr := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusServiceUnavailable}},
		Err:      errors.New("service unavailable"),
	}
	if !retryer.IsErrorRetryable(serverErr) {
		t.Fatal("HTTP 503 is not retryable")
	}

	for attempt := 1; attempt <= 10; attempt++ {
		delay, err := retryer.RetryDelay(attempt, serverErr)
		if err != nil {
			t.Fatalf("RetryDelay(%d) error = %v", attempt, err)
		}
		if delay > s3MaxBackoff {
			t.Fatalf("RetryDelay(%d) = %s, exceeds cap %s", attempt, delay, s3MaxBackoff)
		}
	}
}

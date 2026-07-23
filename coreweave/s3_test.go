package coreweave

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	cwobjectv1connect "buf.build/gen/go/coreweave/cwobject/connectrpc/go/cwobject/v1/cwobjectv1connect"
	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/aws/aws-sdk-go-v2/aws"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
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

			transport, ok := httpClient.Transport.(*retryablehttp.RoundTripper)
			if !ok {
				t.Fatalf("HTTP transport type = %T, want *retryablehttp.RoundTripper", httpClient.Transport)
			}
			if transport.Client.HTTPClient.Timeout != 30*time.Second {
				t.Errorf("HTTP client timeout = %s, want %s",
					transport.Client.HTTPClient.Timeout, 30*time.Second)
			}
			if transport.Client.RetryMax != 10 {
				t.Errorf("retry max = %d, want 10", transport.Client.RetryMax)
			}
			if transport.Client.RetryWaitMin != 200*time.Millisecond {
				t.Errorf("retry wait min = %s, want %s", transport.Client.RetryWaitMin, 200*time.Millisecond)
			}
			if transport.Client.RetryWaitMax != 5*time.Second {
				t.Errorf("retry wait max = %s, want %s", transport.Client.RetryWaitMax, 5*time.Second)
			}
			if reflect.ValueOf(transport.Client.Backoff).Pointer() != reflect.ValueOf(retryablehttp.DefaultBackoff).Pointer() {
				t.Error("retry backoff is not retryablehttp.DefaultBackoff")
			}
			if reflect.ValueOf(transport.Client.CheckRetry).Pointer() != reflect.ValueOf(RetryPolicy).Pointer() {
				t.Error("retry policy is not coreweave.RetryPolicy")
			}

			innerTransport, ok := transport.Client.HTTPClient.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("inner HTTP transport type = %T, want *http.Transport",
					transport.Client.HTTPClient.Transport)
			}
			if !innerTransport.DisableKeepAlives {
				t.Error("inner HTTP transport keep-alives enabled, want disabled")
			}
			if innerTransport.MaxIdleConnsPerHost != -1 {
				t.Errorf("inner HTTP transport max idle connections per host = %d, want -1",
					innerTransport.MaxIdleConnsPerHost)
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

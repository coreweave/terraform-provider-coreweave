package coreweave

import (
	"strings"
	"testing"

	clusterv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// The values below are fake, and every one of them must stay out of the logs.
const (
	secretPassword   = "pa55word-must-not-be-logged"
	secretHeader     = "Bearer must-not-be-logged"
	secretAccessKey  = "SAK-must-not-be-logged"
	secretSession    = "SESSION-must-not-be-logged"
	publicSlug       = "endpoint-slug"
	publicHeaderName = "Authorization"
)

func endpointRefRequest() *clusterv1beta1.CreateEndpointRequest {
	req := &clusterv1beta1.CreateEndpointRequest{}
	ref := &typesv1beta1.ForwardingEndpointRef{}
	ref.SetSlug(publicSlug)
	req.SetRef(ref)
	return req
}

func httpsBasicAuthRequest() *clusterv1beta1.CreateEndpointRequest {
	req := endpointRefRequest()

	basic := &typesv1beta1.BasicAuthCredentials{}
	basic.SetUsername("svc-user")
	basic.SetPassword(secretPassword)

	creds := &typesv1beta1.HTTPSCredentials{}
	creds.SetBasicAuth(basic)
	req.SetHttps(creds)

	return req
}

func httpsAuthHeadersRequest() *clusterv1beta1.CreateEndpointRequest {
	req := endpointRefRequest()

	headers := &typesv1beta1.AuthHeadersCredentials{}
	headers.SetHeaders(map[string]string{publicHeaderName: secretHeader})

	creds := &typesv1beta1.HTTPSCredentials{}
	creds.SetAuthHeaders(headers)
	req.SetHttps(creds)

	return req
}

func s3CredentialsRequest() *clusterv1beta1.CreateEndpointRequest {
	req := endpointRefRequest()

	creds := &typesv1beta1.S3Credentials{}
	creds.SetAccessKeyId("AKIA-public-enough")
	creds.SetSecretAccessKey(secretAccessKey)
	creds.SetSessionToken(secretSession)
	req.SetS3(creds)

	return req
}

// TestRedactCredentialsKeepsSecretsOutOfLogs is the regression guard for
// credential values reaching tflog. Terraform's Sensitive and WriteOnly flags
// do not redact custom log fields, so the interceptor has to do it.
func TestRedactCredentialsKeepsSecretsOutOfLogs(t *testing.T) {
	t.Parallel()

	secrets := []string{secretPassword, secretHeader, secretAccessKey, secretSession}

	tests := []struct {
		name    string
		message proto.Message
	}{
		{name: "https basic auth", message: httpsBasicAuthRequest()},
		{name: "https auth headers", message: httpsAuthHeadersRequest()},
		{name: "s3 credentials", message: s3CredentialsRequest()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			redacted, hadCredentials := redactCredentials(tc.message)
			require.True(t, hadCredentials, "credentials oneof should have been detected")

			formatted := logFormatMessage(redacted)
			for _, secret := range secrets {
				assert.NotContains(t, formatted, secret,
					"credential value reached the log line: %s", formatted)
			}

			// The non-secret part of the request still has to be loggable, or
			// the redaction has destroyed the diagnostic value of the log.
			assert.Contains(t, formatted, publicSlug)
		})
	}
}

// TestRedactCredentialsLeavesOtherMessagesAlone keeps the redaction from
// quietly stripping fields on requests that carry no credentials.
func TestRedactCredentialsLeavesOtherMessagesAlone(t *testing.T) {
	t.Parallel()

	req := endpointRefRequest()

	redacted, hadCredentials := redactCredentials(req)
	assert.False(t, hadCredentials)
	assert.Same(t, req, redacted, "a message without credentials should not be cloned")
	assert.Contains(t, logFormatMessage(redacted), publicSlug)
}

// TestRedactCredentialsDoesNotMutateTheRequest matters because the message
// being logged is the one about to go on the wire.
func TestRedactCredentialsDoesNotMutateTheRequest(t *testing.T) {
	t.Parallel()

	req := httpsAuthHeadersRequest()

	_, hadCredentials := redactCredentials(req)
	require.True(t, hadCredentials)

	headers := req.GetHttps().GetAuthHeaders().GetHeaders()
	require.Contains(t, headers, publicHeaderName)
	assert.Equal(t, secretHeader, headers[publicHeaderName],
		"redaction must not clear credentials on the outgoing request")
}

// TestLogFormatMessageWithoutRedactionStillCarriesSecrets pins the reason the
// redaction exists: prototext has no notion of sensitivity.
func TestLogFormatMessageWithoutRedactionStillCarriesSecrets(t *testing.T) {
	t.Parallel()

	formatted := logFormatMessage(httpsBasicAuthRequest())
	require.True(t, strings.Contains(formatted, secretPassword),
		"prototext is expected to serialize credentials verbatim; if this fails the redaction may no longer be needed")
}

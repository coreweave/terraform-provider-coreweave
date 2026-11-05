package coreweave

import (
	"strings"
	"testing"

	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	clusterv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Fake values that must stay out of the logs.
const (
	secretPassword   = "pa55word-must-not-be-logged"
	secretHeader     = "Bearer must-not-be-logged"
	secretAccessKey  = "SAK-must-not-be-logged"
	secretSession    = "SESSION-must-not-be-logged"
	publicSlug       = "endpoint-slug"
	publicHeaderName = "Authorization"
)

// Registration is by string, so a renamed field would stop being redacted
// silently.
func TestRedactedFieldsResolve(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, redactedFields)

	for messageName, fieldNames := range redactedFields {
		descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(messageName)
		require.NoErrorf(t, err, "%s is not a linked proto message", messageName)

		message, ok := descriptor.(protoreflect.MessageDescriptor)
		require.Truef(t, ok, "%s is not a message", messageName)
		require.NotEmptyf(t, fieldNames, "%s registers no fields", messageName)

		for _, fieldName := range fieldNames {
			assert.NotNilf(t, message.Fields().ByName(fieldName),
				"%s has no field %s; update redactedFields", messageName, fieldName)
		}
	}
}

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

func accessKeyResponse() *cwobjectv1.CreateAccessKeyFromJWTResponse {
	resp := &cwobjectv1.CreateAccessKeyFromJWTResponse{}
	resp.SetAccessKeyId("AKIA-public-enough")
	resp.SetSecretKey(secretAccessKey)
	return resp
}

func TestRedactCredentialsKeepsSecretsOutOfLogs(t *testing.T) {
	t.Parallel()

	secrets := []string{secretPassword, secretHeader, secretAccessKey, secretSession}

	tests := []struct {
		name    string
		message proto.Message
		cleared []string
	}{
		{name: "https basic auth", message: httpsBasicAuthRequest(), cleared: []string{"https"}},
		{name: "https auth headers", message: httpsAuthHeadersRequest(), cleared: []string{"https"}},
		{name: "s3 credentials", message: s3CredentialsRequest(), cleared: []string{"s3"}},
		{name: "object storage access key", message: accessKeyResponse(), cleared: []string{"secret_key"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			redacted, cleared := redactCredentials(tc.message)
			assert.Equal(t, tc.cleared, cleared)

			formatted := logFormatMessage(redacted)
			for _, secret := range secrets {
				assert.NotContains(t, formatted, secret,
					"credential value reached the log line: %s", formatted)
			}
		})
	}
}

func TestRedactCredentialsLeavesOtherMessagesAlone(t *testing.T) {
	t.Parallel()

	req := endpointRefRequest()

	redacted, cleared := redactCredentials(req)
	assert.Empty(t, cleared, "a registered message with no credentials set clears nothing")
	assert.Same(t, req, redacted, "nothing to clear should not copy")
	assert.Contains(t, logFormatMessage(redacted), publicSlug)

	// A redacted message still has to be loggable.
	withSecret, cleared := redactCredentials(httpsBasicAuthRequest())
	assert.NotEmpty(t, cleared)
	assert.Contains(t, logFormatMessage(withSecret), publicSlug)
}

func TestRedactCredentialsDoesNotMutateTheMessage(t *testing.T) {
	t.Parallel()

	req := httpsAuthHeadersRequest()

	_, cleared := redactCredentials(req)
	require.NotEmpty(t, cleared)

	headers := req.GetHttps().GetAuthHeaders().GetHeaders()
	require.Contains(t, headers, publicHeaderName)
	assert.Equal(t, secretHeader, headers[publicHeaderName],
		"redaction must not clear credentials on the outgoing message")
}

// Pins the reason the registry exists.
func TestLogFormatMessageWithoutRedactionStillCarriesSecrets(t *testing.T) {
	t.Parallel()

	formatted := logFormatMessage(httpsBasicAuthRequest())
	require.True(t, strings.Contains(formatted, secretPassword),
		"prototext is expected to serialize credentials verbatim; if this fails the redaction may no longer be needed")
}

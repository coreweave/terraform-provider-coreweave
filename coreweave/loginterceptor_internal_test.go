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
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Fake values that must stay out of the logs.
const (
	secretPassword     = "pa55word-must-not-be-logged"
	secretHeader       = "Bearer must-not-be-logged"
	secretAccessKey    = "SAK-must-not-be-logged"
	secretSession      = "SESSION-must-not-be-logged"
	secretSessionToken = "TOKEN-must-not-be-logged"

	// Values that must survive, so an over-broad redaction fails too.
	publicSlug        = "endpoint-slug"
	publicHeaderName  = "Authorization"
	publicAccessKeyID = "AKIA-public-enough"
	publicPrincipal   = "svc-principal"

	// Names redactCredentials reports as cleared.
	clearedHTTPS = "https"
	clearedS3    = "s3"
)

// secretValues is every fake credential above; no formatted log line may contain any.
var secretValues = []string{
	secretPassword, secretHeader, secretAccessKey, secretSession, secretSessionToken,
}

// Registration is by string, so a rename would silently stop redacting.
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
	creds.SetAccessKeyId(publicAccessKeyID)
	creds.SetSecretAccessKey(secretAccessKey)
	creds.SetSessionToken(secretSession)
	req.SetS3(creds)

	return req
}

func accessKeyResponse() *cwobjectv1.CreateAccessKeyFromJWTResponse {
	resp := &cwobjectv1.CreateAccessKeyFromJWTResponse{}
	resp.SetAccessKeyId(publicAccessKeyID)
	resp.SetSecretKey(secretAccessKey)
	resp.SetPrincipalName(publicPrincipal)
	return resp
}

func samlAccessKeyResponse() *cwobjectv1.CreateAccessKeyFromSAMLResponse {
	resp := &cwobjectv1.CreateAccessKeyFromSAMLResponse{}
	resp.SetAccessKeyId(publicAccessKeyID)
	resp.SetSecretKey(secretAccessKey)
	resp.SetPrincipalName(publicPrincipal)
	return resp
}

func oidcAccessKeyResponse() *cwobjectv1.CreateAccessKeyFromOIDCResponse {
	resp := &cwobjectv1.CreateAccessKeyFromOIDCResponse{}
	resp.SetAccessKeyId(publicAccessKeyID)
	resp.SetSecretKey(secretAccessKey)
	resp.SetPrincipalName(publicPrincipal)
	return resp
}

func apiTokenAccessKeyResponse() *cwobjectv1.CreateAccessKeyFromApiTokenResponse {
	resp := &cwobjectv1.CreateAccessKeyFromApiTokenResponse{}
	resp.SetAccessKeyId(publicAccessKeyID)
	resp.SetSecretAccessKey(secretAccessKey)
	resp.SetToken(wrapperspb.String(secretSessionToken))
	return resp
}

func containerCredsAccessKeyResponse() *cwobjectv1.CreateAccessKeyFromContainerCredsResponse {
	resp := &cwobjectv1.CreateAccessKeyFromContainerCredsResponse{}
	resp.SetAccessKeyId(publicAccessKeyID)
	resp.SetSecretAccessKey(secretAccessKey)
	resp.SetToken(wrapperspb.String(secretSessionToken))
	return resp
}

// asUpdate rebuilds a create request as the update that carries the same
// credential oneof, so both registered endpoint messages get exercised.
func asUpdate(create *clusterv1beta1.CreateEndpointRequest) proto.Message {
	update := &clusterv1beta1.UpdateEndpointRequest{}
	update.SetRef(create.GetRef())
	update.SetCredentialAction(clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_REPLACE)
	if https := create.GetHttps(); https != nil {
		update.SetHttps(https)
	} else {
		update.SetS3(create.GetS3())
	}
	return update
}

// redactionCase states, per registered message, which fields redaction reports
// and which values must still reach the log. remains is what keeps the
// assertion honest where a message is mostly credential: it names the exact
// output expected instead of merely requiring some output.
type redactionCase struct {
	name    string
	message proto.Message
	cleared []string
	remains []string
}

func redactionCases() []redactionCase {
	return []redactionCase{
		{
			name:    "create https basic auth",
			message: httpsBasicAuthRequest(),
			cleared: []string{clearedHTTPS},
			remains: []string{publicSlug},
		},
		{
			name:    "create https auth headers",
			message: httpsAuthHeadersRequest(),
			cleared: []string{clearedHTTPS},
			remains: []string{publicSlug},
		},
		{
			name:    "create s3 credentials",
			message: s3CredentialsRequest(),
			cleared: []string{clearedS3},
			remains: []string{publicSlug},
		},
		{
			name:    "update https basic auth",
			message: asUpdate(httpsBasicAuthRequest()),
			cleared: []string{clearedHTTPS},
			remains: []string{publicSlug},
		},
		{
			name:    "update https auth headers",
			message: asUpdate(httpsAuthHeadersRequest()),
			cleared: []string{clearedHTTPS},
			remains: []string{publicSlug},
		},
		{
			name:    "update s3 credentials",
			message: asUpdate(s3CredentialsRequest()),
			cleared: []string{clearedS3},
			remains: []string{publicSlug},
		},
		{
			name:    "access key from jwt",
			message: accessKeyResponse(),
			cleared: []string{string(fieldSecretKey)},
			remains: []string{publicAccessKeyID, publicPrincipal},
		},
		{
			name:    "access key from saml",
			message: samlAccessKeyResponse(),
			cleared: []string{string(fieldSecretKey)},
			remains: []string{publicAccessKeyID, publicPrincipal},
		},
		{
			name:    "access key from oidc",
			message: oidcAccessKeyResponse(),
			cleared: []string{string(fieldSecretKey)},
			remains: []string{publicAccessKeyID, publicPrincipal},
		},
		{
			name:    "access key from api token",
			message: apiTokenAccessKeyResponse(),
			cleared: []string{string(fieldSecretAccessKey), string(fieldToken)},
			remains: []string{publicAccessKeyID},
		},
		{
			name:    "access key from container creds",
			message: containerCredsAccessKeyResponse(),
			cleared: []string{string(fieldSecretAccessKey), string(fieldToken)},
			remains: []string{publicAccessKeyID},
		},
	}
}

// Every registered message needs a case, or a new registration ships untested.
func TestRedactionCasesCoverRegistry(t *testing.T) {
	t.Parallel()

	covered := map[protoreflect.FullName]bool{}
	for _, tc := range redactionCases() {
		covered[tc.message.ProtoReflect().Descriptor().FullName()] = true
	}

	for messageName := range redactedFields {
		assert.Truef(t, covered[messageName], "%s has no redaction case", messageName)
	}
}

func TestRedactCredentialsKeepsSecretsOutOfLogs(t *testing.T) {
	t.Parallel()

	for _, tc := range redactionCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			redacted, cleared := redactCredentials(tc.message)
			assert.Equal(t, tc.cleared, cleared)

			formatted := logFormatMessage(redacted)
			for _, secret := range secretValues {
				assert.NotContains(t, formatted, secret,
					"credential value reached the log line: %s", formatted)
			}
		})
	}
}

// TestLogMessageFieldsRedacts drives the function the interceptor calls. The
// tests above call redactCredentials directly, so they pass even if
// logMessageFields logs the unredacted message.
func TestLogMessageFieldsRedacts(t *testing.T) {
	t.Parallel()

	for _, tc := range redactionCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			original := proto.Clone(tc.message)

			fields := map[string]any{}
			logMessageFields(fields, tc.message)

			assert.Equal(t, strings.Join(tc.cleared, ","), fields[logRedactedKey])
			assert.True(t, proto.Equal(original, tc.message),
				"logging must not strip credentials from the outgoing request")

			formatted, ok := fields[logMessageKey].(string)
			require.True(t, ok, "no message field was produced")
			for _, secret := range secretValues {
				assert.NotContains(t, formatted, secret, "credential value reached the log field")
			}
			for _, remains := range tc.remains {
				assert.Contains(t, formatted, remains, "redaction cleared more than the credential")
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

func TestLogFormatMessageWithoutRedactionStillCarriesSecrets(t *testing.T) {
	t.Parallel()

	formatted := logFormatMessage(httpsBasicAuthRequest())
	require.True(t, strings.Contains(formatted, secretPassword),
		"prototext is expected to serialize credentials verbatim; if this fails the redaction may no longer be needed")
}

//nolint:goconst // Repeated values make independent test cases easier to read.
package serviceaccount

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadOIDCToken(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "oidc-token")
	require.NoError(t, os.WriteFile(file, []byte("  file-oidc-token\n"), 0o600))

	tests := map[string]struct {
		tokenSource      func(context.Context) (string, error)
		expectedResponse string
		expectedError    error
	}{
		"trims an OIDC token read from a file": {
			tokenSource: func(ctx context.Context) (string, error) {
				return readOIDCTokenFile(ctx, file)
			},
			expectedResponse: "file-oidc-token",
		},
		"rejects oversized OIDC tokens": {
			tokenSource: func(context.Context) (string, error) {
				return strings.Repeat("x", 65537), nil
			},
			expectedError: errors.New("OIDC token exceeds the size limit"),
		},
		"rejects empty OIDC tokens": {
			tokenSource: func(context.Context) (string, error) {
				return "  ", nil
			},
			expectedError: errors.New("OIDC token is empty"),
		},
		"reports missing OIDC token source": {
			expectedError: errors.New("OIDC token source is not configured"),
		},
		"sanitizes source errors": {
			tokenSource: func(context.Context) (string, error) {
				return "", errors.New("raw credential")
			},
			expectedError: errors.New("cannot read OIDC token"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := &Provider{options: Options{TokenSource: tt.tokenSource}}
			actual, err := provider.readOIDCToken(t.Context())
			if tt.expectedError != nil {
				require.EqualError(t, err, tt.expectedError.Error())
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expectedResponse, actual)
		})
	}
}

func TestReadOIDCTokenFromEnvironmentCommand(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("printf"); err != nil {
		t.Skip("printf is unavailable")
	}
	tests := map[string]struct {
		stdout           string
		expectedResponse string
	}{
		"extracts the ExecCredential token": {
			stdout:           `{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":"exec-token"}}`,
			expectedResponse: "exec-token",
		},
		"falls back when status.token is blank": {
			stdout:           `{"kind":"ExecCredential","status":{"token":""}}`,
			expectedResponse: `{"kind":"ExecCredential","status":{"token":""}}`,
		},
		"falls back when decoding fails": {
			stdout:           `{"status":`,
			expectedResponse: `{"status":`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			environment := map[string]string{
				"COREWEAVE_SERVICE_ACCOUNT_ID":        "account",
				"COREWEAVE_SERVICE_ACCOUNT_ORG_ID":    "org",
				"COREWEAVE_SERVICE_ACCOUNT_TOKEN_CMD": "printf\t%s\\n  " + tt.stdout,
			}
			provider, err := Detect(func(name string) string { return environment[name] })
			require.NoError(t, err)
			require.NotNil(t, provider)
			t.Cleanup(provider.Close)

			token, err := provider.readOIDCToken(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.expectedResponse, token)
		})
	}
}

func TestRunOIDCTokenCommand(t *testing.T) {
	t.Parallel()

	const filename = "token output.json"
	const rawOutput = ` {"kind":"ExecCredential","status":{"token":"oidc-token"}}` + "\n"
	tests := map[string]struct {
		args             []string
		stdout           string
		expectedResponse []byte
		expectedError    error
	}{
		"requires an argument vector": {
			expectedError: errors.New("OIDC token command requires an executable"),
		},
		"requires a nonblank executable": {
			args:          []string{" "},
			expectedError: errors.New("OIDC token command requires an executable"),
		},
		"uses the working directory and preserves arguments and stdout": {
			args:             []string{"cat", filename},
			stdout:           rawOutput,
			expectedResponse: []byte(rawOutput),
		},
		"rejects oversized stdout": {
			args:          []string{"cat", filename},
			stdout:        strings.Repeat("x", 65537),
			expectedError: errors.New("OIDC token exceeds the size limit"),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if tt.stdout != "" {
				if _, err := exec.LookPath("cat"); err != nil {
					t.Skip("cat is unavailable")
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(tt.stdout), 0o600))
			}
			actual, err := RunOIDCTokenCommand(t.Context(), tt.args, dir)
			if tt.expectedError != nil {
				require.EqualError(t, err, tt.expectedError.Error())
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expectedResponse, actual)
		})
	}
}

package serviceaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// maxOIDCTokenBytes caps raw token input at 64 KiB to bound buffering for files and commands.
const maxOIDCTokenBytes = 64 << 10

func (p *Provider) readOIDCToken(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if p.options.TokenSource == nil {
		return "", errors.New("OIDC token source is not configured")
	}
	raw, err := p.options.TokenSource(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if err != nil {
		return "", errors.New("cannot read OIDC token")
	}
	if len(raw) > maxOIDCTokenBytes {
		return "", errors.New("OIDC token exceeds the size limit")
	}
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", errors.New("OIDC token is empty")
	}
	return token, nil
}

func readOIDCTokenFile(ctx context.Context, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("OIDC token path must resolve to a regular file")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	file, err := os.Open(path) //nolint:gosec // Reading the explicitly configured token file is the feature.
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("OIDC token path must resolve to a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxOIDCTokenBytes+1))
	return string(raw), err
}

func readOIDCTokenCommand(ctx context.Context, args []string, dir string) (string, error) {
	stdout, err := RunOIDCTokenCommand(ctx, args, dir)
	if err != nil {
		return "", err
	}
	var credential struct {
		Status struct {
			Token string `json:"token"`
		} `json:"status"`
	}
	if err := json.Unmarshal(stdout, &credential); err == nil && strings.TrimSpace(credential.Status.Token) != "" {
		return credential.Status.Token, nil
	}
	return string(stdout), nil
}

// RunOIDCTokenCommand runs args[0] with the remaining arguments and returns raw
// stdout, rejecting output larger than 64 KiB. An empty dir uses the current
// working directory. Use a context deadline to bound execution time.
func RunOIDCTokenCommand(ctx context.Context, args []string, dir string) ([]byte, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil, errors.New("OIDC token command requires an executable")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // The caller explicitly configures the token command.
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	var stdout oidcTokenOutput
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if stdout.err != nil {
			return nil, stdout.err
		}
		return nil, err
	}
	return stdout.buffer.Bytes(), nil
}

type oidcTokenOutput struct {
	buffer bytes.Buffer
	err    error
}

func (out *oidcTokenOutput) Write(p []byte) (int, error) {
	if out.buffer.Len()+len(p) > maxOIDCTokenBytes {
		out.err = errors.New("OIDC token exceeds the size limit")
		return 0, out.err
	}
	return out.buffer.Write(p)
}

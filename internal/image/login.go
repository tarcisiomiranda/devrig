package image

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// LoginResult is the JSON shape for login. It never carries the password.
type LoginResult struct {
	Tool     string    `json:"tool"`
	Registry string    `json:"registry"`
	User     string    `json:"user"`
	Source   string    `json:"source"`
	Keys     LoginKeys `json:"keys"`
}

// LoginKeys names where the user and the password were read from.
type LoginKeys struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// Login runs `<tool> login --username U --password-stdin REGISTRY`. The
// password travels on stdin only: never argv, never output, never an error.
func (c *Client) Login(ctx context.Context, cred *Credentials) (*LoginResult, error) {
	if cred == nil || cred.User == "" || cred.Password == "" {
		return nil, errors.New("login needs a user and a password")
	}
	if cred.Registry == "" {
		return nil, errors.New("login needs a registry: pass it as an argument or set REGISTRY in the env file")
	}
	args := append([]string{"login", "--username", cred.User, "--password-stdin"}, c.tlsArgs(cred.Registry)...)
	args = append(args, cred.Registry)
	if _, err := c.r.run(ctx, []byte(cred.Password), args...); err != nil {
		return nil, fmt.Errorf("login to %s as %s failed: %w", cred.Registry, cred.User, redact(err, cred.Password))
	}
	return &LoginResult{
		Tool:     c.Tool().String(),
		Registry: cred.Registry,
		User:     cred.User,
		Source:   cred.Source,
		Keys:     LoginKeys{User: cred.UserKey, Password: cred.PasswordKey},
	}, nil
}

// loopback reports whether a registry is on this machine. Docker treats
// 127.0.0.0/8 and localhost as insecure by default; Podman needs to be told.
func loopback(registry string) bool {
	host := registry
	if h, _, err := net.SplitHostPort(registry); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// tlsArgs disables TLS verification for a loopback registry on Podman, which
// is what Docker already does on its own.
func (c *Client) tlsArgs(registry string) []string {
	if c.Tool() == ToolPodman && loopback(registry) {
		return []string{"--tls-verify=false"}
	}
	return nil
}

// redact replaces the secret in an error message, should a tool ever echo it.
func redact(err error, secret string) error {
	if err == nil || secret == "" || !strings.Contains(err.Error(), secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "<redacted>"))
}

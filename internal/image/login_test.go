package image

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testCreds() *Credentials {
	return &Credentials{
		Registry: "ghcr.io", User: "alice", Password: "s3cr3t-token",
		Source: ".env", UserKey: "REGISTRY_USER", PasswordKey: "REGISTRY_TOKEN",
	}
}

func TestLoginPassesThePasswordOnStdinOnly(t *testing.T) {
	c, f := testClient(ToolPodman)
	res, err := c.Login(context.Background(), testCreds())
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := f.argv(); len(got) != 1 || got[0] != "login --username alice --password-stdin ghcr.io" {
		t.Fatalf("argv = %v", got)
	}
	if string(f.calls[0].stdin) != "s3cr3t-token" {
		t.Fatalf("stdin = %q", f.calls[0].stdin)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "s3cr3t") {
		t.Fatalf("JSON leaks the password: %s", raw)
	}
	if res.Tool != "podman" || res.Registry != "ghcr.io" || res.User != "alice" || res.Source != ".env" ||
		res.Keys.User != "REGISTRY_USER" || res.Keys.Password != "REGISTRY_TOKEN" {
		t.Fatalf("result = %+v", res)
	}
}

func TestLoginFailureNamesRegistryAndUserButNotThePassword(t *testing.T) {
	c, f := testClient(ToolDocker)
	f.err["login"] = errors.New("unauthorized: bad token s3cr3t-token")
	_, err := c.Login(context.Background(), testCreds())
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ghcr.io") || !strings.Contains(msg, "alice") {
		t.Fatalf("error lacks registry/user: %s", msg)
	}
	if strings.Contains(msg, "s3cr3t") {
		t.Fatalf("error leaks the password: %s", msg)
	}
}

func TestLoginRequiresARegistry(t *testing.T) {
	c, f := testClient(ToolPodman)
	cred := testCreds()
	cred.Registry = ""
	if _, err := c.Login(context.Background(), cred); err == nil || !strings.Contains(err.Error(), "registry") {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("the tool must not run without a registry")
	}
}

// Docker treats loopback registries as insecure by default; Podman must be told.
func TestLoginDisablesTLSVerifyOnlyForPodmanLoopback(t *testing.T) {
	for _, tc := range []struct {
		tool     Tool
		registry string
		want     string
	}{
		{ToolPodman, "localhost:5000", "login --username alice --password-stdin --tls-verify=false localhost:5000"},
		{ToolPodman, "127.0.0.1:5000", "login --username alice --password-stdin --tls-verify=false 127.0.0.1:5000"},
		{ToolPodman, "[::1]:5000", "login --username alice --password-stdin --tls-verify=false [::1]:5000"},
		{ToolPodman, "ghcr.io", "login --username alice --password-stdin ghcr.io"},
		{ToolDocker, "localhost:5000", "login --username alice --password-stdin localhost:5000"},
	} {
		c, f := testClient(tc.tool)
		cred := testCreds()
		cred.Registry = tc.registry
		if _, err := c.Login(context.Background(), cred); err != nil {
			t.Fatalf("%s %s: %v", tc.tool, tc.registry, err)
		}
		if got := f.argv()[0]; got != tc.want {
			t.Errorf("%s %s: argv = %q, want %q", tc.tool, tc.registry, got, tc.want)
		}
	}
}

func TestLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "localhost:5000": true, "127.0.0.1": true, "127.0.0.1:5000": true,
		"[::1]:5000": true, "ghcr.io": false, "registry.example.com:5000": false, "10.0.0.5:5000": false,
	} {
		if got := loopback(host); got != want {
			t.Errorf("loopback(%q) = %v, want %v", host, got, want)
		}
	}
}

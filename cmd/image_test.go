package cmd

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/tarcisiomiranda/devrig/internal/image"
)

func TestResolveEnvFile(t *testing.T) {
	exists := func(p string) bool { return p == ".env" }
	missing := func(string) bool { return false }
	if got := resolveEnvFile("", exists); got != ".env" {
		t.Fatalf("default with .env present = %q", got)
	}
	if got := resolveEnvFile("", missing); got != "" {
		t.Fatalf("default without .env = %q, want none", got)
	}
	if got := resolveEnvFile("deploy/.env", missing); got != "deploy/.env" {
		t.Fatalf("explicit = %q, want it returned even when absent so Read can report it", got)
	}
}

func TestCredentialSourcesWithNoLoginReadNothing(t *testing.T) {
	sources, err := credentialSources(context.Background(), "definitely-missing.env", true)
	if err != nil || sources != nil {
		t.Fatalf("sources = %v, err = %v; --no-login must read nothing", sources, err)
	}
}

func TestCredentialSourcesAlwaysEndWithTheEnvironment(t *testing.T) {
	t.Chdir(t.TempDir()) // no .env here, so confctl is never needed
	sources, err := credentialSources(context.Background(), "", false)
	if err != nil {
		t.Fatalf("credentialSources: %v", err)
	}
	if len(sources) != 1 || sources[0].Name != image.EnvironmentSourceName {
		t.Fatalf("sources = %+v", sources)
	}
}

func TestExplicitMissingEnvFileIsAnError(t *testing.T) {
	// DEVRIG_CONFCTL points at a binary that does not exist so this test
	// proves the ordering: the file-existence check must win over the
	// missing-confctl error, not the other way around.
	t.Setenv("DEVRIG_CONFCTL", "confctl-not-installed-for-this-test")
	_, err := credentialSources(context.Background(), "definitely-missing.env", false)
	if err == nil {
		t.Fatal("an explicit --env-file that does not exist must fail")
	}
	if !strings.Contains(err.Error(), "env file") {
		t.Fatalf("err = %v, want the missing file reported, not a confctl install hint", err)
	}
	// MissingBinaryError wraps an exec.LookPath error, never fs.ErrNotExist,
	// so this only passes when the stat in credentialSources runs before
	// envfile.NewRunner — i.e. it discriminates the ordering the substring
	// check above cannot.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want it to wrap fs.ErrNotExist (proves the file stat ran before confctl was resolved)", err)
	}
}

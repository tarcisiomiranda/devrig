package instance

import (
	"testing"

	"github.com/tarcisiomiranda/devrig/internal/engine"
)

func TestValidateName(t *testing.T) {
	ok := []string{"mfe-platform", "a", "cyber_asm", "X1"}
	for _, n := range ok {
		if err := ValidateName(n); err != nil {
			t.Fatalf("ValidateName(%q): %v", n, err)
		}
	}
	bad := []string{"", "-x", "_x", "has space", "a/b"}
	for _, n := range bad {
		if err := ValidateName(n); err == nil {
			t.Fatalf("ValidateName(%q) expected error", n)
		}
	}
}

func TestDefaultDatabase(t *testing.T) {
	got := DefaultDatabase("mfe-platform")
	want := "mfe_platform_test"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSanitizeForDBTruncates(t *testing.T) {
	long := ""
	for len(long) < 80 {
		long += "abcdefghij"
	}
	if got := SanitizeForDB(long); len(got) != 48 {
		t.Fatalf("expected truncation to 48, got %d", len(got))
	}
}

func TestContainerName(t *testing.T) {
	if got := ContainerName("mfe-platform"); got != "devrig-mfe_platform" {
		t.Fatalf("unexpected: %s", got)
	}
	if got := LegacyContainerName("mfe-platform"); got != "testpg-mfe_platform" {
		t.Fatalf("unexpected legacy: %s", got)
	}
}

func TestURLPostgresKeepsSSLModeDisable(t *testing.T) {
	spec := engine.Default()
	u := URL(spec, "test", "test", "127.0.0.1", 54321, "cyber_mfe_test")
	want := "postgres://test:test@127.0.0.1:54321/cyber_mfe_test?sslmode=disable"
	if u != want {
		t.Fatalf("got %q want %q", u, want)
	}
}

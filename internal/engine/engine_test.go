package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestLookupDefaultsToPostgres(t *testing.T) {
	spec, err := Lookup("")
	if err != nil {
		t.Fatalf("Lookup(\"\"): %v", err)
	}
	if spec.Name != Postgres {
		t.Fatalf("default engine = %q, want postgres", spec.Name)
	}
}

func TestLookupPostgresIsImplemented(t *testing.T) {
	for _, in := range []string{"postgres", "POSTGRES", " Postgres "} {
		spec, err := Lookup(in)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", in, err)
		}
		if spec.ContainerPort != 5432 {
			t.Fatalf("port = %d, want 5432", spec.ContainerPort)
		}
	}
}

func TestPlannedEnginesAreDeclaredButRefused(t *testing.T) {
	for _, name := range []string{"mysql", "mariadb"} {
		spec, err := Lookup(name)
		if spec != nil {
			t.Fatalf("Lookup(%q) returned a usable spec before implementation", name)
		}
		var unsupported *ErrUnsupported
		if !errors.As(err, &unsupported) {
			t.Fatalf("Lookup(%q) error = %v, want ErrUnsupported", name, err)
		}
		// The message must tell the user what does work.
		if !strings.Contains(err.Error(), "postgres") {
			t.Fatalf("error should mention the working engines: %v", err)
		}
	}
}

func TestUnknownEngineIsNotUnsupported(t *testing.T) {
	_, err := Lookup("oracle")
	if err == nil {
		t.Fatal("expected an error for an unknown engine")
	}
	var unsupported *ErrUnsupported
	if errors.As(err, &unsupported) {
		t.Fatal("unknown engine must not be reported as merely unimplemented")
	}
	// The CLI relies on this type to offer the pre-v0.2.0 migration hint.
	var unknown *ErrUnknown
	if !errors.As(err, &unknown) {
		t.Fatalf("Lookup(\"oracle\") error = %T, want *ErrUnknown", err)
	}
	if unknown.Name != "oracle" {
		t.Fatalf("ErrUnknown.Name = %q, want oracle", unknown.Name)
	}
}

func TestKnownIncludesPlanned(t *testing.T) {
	known := strings.Join(Known(), ",")
	for _, want := range []string{"postgres", "mysql", "mariadb"} {
		if !strings.Contains(known, want) {
			t.Fatalf("Known() = %s, missing %s", known, want)
		}
	}
	if impl := Implemented(); len(impl) != 1 || impl[0] != "postgres" {
		t.Fatalf("Implemented() = %v, want [postgres]", impl)
	}
}

func TestPostgresEnvAndURL(t *testing.T) {
	spec := Default()
	env := strings.Join(spec.Env("u", "p", "d"), " ")
	for _, want := range []string{"POSTGRES_USER=u", "POSTGRES_PASSWORD=p", "POSTGRES_DB=d"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env = %q, missing %q", env, want)
		}
	}
	// sslmode=disable is required: local throwaway Postgres has no TLS.
	if got := spec.URL("u", "p", "127.0.0.1", 5555, "d"); !strings.HasSuffix(got, "?sslmode=disable") {
		t.Fatalf("URL = %q, must end with ?sslmode=disable", got)
	}
}

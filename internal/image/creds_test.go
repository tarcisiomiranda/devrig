package image

import (
	"encoding/json"
	"strings"
	"testing"
)

func fileSource(kv ...string) Source {
	s := Source{Name: ".env", Values: map[string]string{}}
	for i := 0; i+1 < len(kv); i += 2 {
		s.Values[kv[i]] = kv[i+1]
	}
	return s
}

func envSource(kv ...string) Source {
	s := SourceFromEnviron(nil)
	for i := 0; i+1 < len(kv); i += 2 {
		s.Values[kv[i]] = kv[i+1]
	}
	return s
}

func TestResolveFollowsKeyOrder(t *testing.T) {
	src := fileSource("DOCKER_USER", "late", "REGISTRY_USER", "early", "REGISTRY_TOKEN", "tok", "DOCKER_PASSWORD", "other")
	cred, err := Resolve("ghcr.io", KeyOverrides{}, []Source{src})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.User != "early" || cred.UserKey != "REGISTRY_USER" {
		t.Fatalf("user = %q from %q, want early from REGISTRY_USER", cred.User, cred.UserKey)
	}
	if cred.Password != "tok" || cred.PasswordKey != "REGISTRY_TOKEN" || cred.Source != ".env" {
		t.Fatalf("password from %q/%q", cred.PasswordKey, cred.Source)
	}
	if cred.Registry != "ghcr.io" {
		t.Fatalf("registry = %q", cred.Registry)
	}
}

// A project's .env is more specific than the shell, whatever key it uses.
func TestResolveFileBeatsEnvironmentEvenWithALaterKey(t *testing.T) {
	file := fileSource("DOCKER_USER", "from-file", "DOCKER_PASSWORD", "file-pw")
	env := envSource("REGISTRY_USER", "from-env", "REGISTRY_TOKEN", "env-pw")
	cred, err := Resolve("", KeyOverrides{}, []Source{file, env})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.User != "from-file" || cred.Password != "file-pw" || cred.Source != ".env" {
		t.Fatalf("got %+v, want the file to win", cred)
	}
}

func TestResolveFallsBackToEnvironment(t *testing.T) {
	file := fileSource("UNRELATED", "x")
	env := envSource("REGISTRY_USER", "ci", "REGISTRY_TOKEN", "ci-pw")
	cred, err := Resolve("", KeyOverrides{}, []Source{file, env})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.User != "ci" || cred.Source != EnvironmentSourceName {
		t.Fatalf("got %+v", cred)
	}
}

func TestResolveFillsRegistryFromKeys(t *testing.T) {
	src := fileSource("REGISTRY", "registry.example.com", "REGISTRY_USER", "u", "REGISTRY_TOKEN", "p")
	cred, err := Resolve("", KeyOverrides{}, []Source{src})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Registry != "registry.example.com" {
		t.Fatalf("registry = %q", cred.Registry)
	}
}

func TestResolveGivenRegistryWins(t *testing.T) {
	src := fileSource("REGISTRY", "registry.example.com", "REGISTRY_USER", "u", "REGISTRY_TOKEN", "p")
	cred, err := Resolve("ghcr.io", KeyOverrides{}, []Source{src})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Registry != "ghcr.io" {
		t.Fatalf("registry = %q, want the one derived from the image", cred.Registry)
	}
}

func TestResolveOverridesReplaceTheKeyLists(t *testing.T) {
	src := fileSource("GH_USER", "octo", "GH_PASS", "pw", "REGISTRY_USER", "ignored", "REGISTRY_TOKEN", "ignored")
	cred, err := Resolve("ghcr.io", KeyOverrides{User: "GH_USER", Password: "GH_PASS"}, []Source{src})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.User != "octo" || cred.Password != "pw" || cred.UserKey != "GH_USER" || cred.PasswordKey != "GH_PASS" {
		t.Fatalf("got %+v", cred)
	}
}

func TestResolveNothingFoundListsEverythingSearched(t *testing.T) {
	_, err := Resolve("ghcr.io", KeyOverrides{}, []Source{fileSource(), envSource()})
	if !IsNoCredentials(err) {
		t.Fatalf("err = %v, want NoCredentialsError", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"ghcr.io", ".env and environment", "REGISTRY_USER", "DOCKER_PASSWORD",
		"confctl set .env REGISTRY_USER=<user> REGISTRY_TOKEN=<token>", "--user-key",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
}

func TestResolvePartialCredentialsNameTheMissingKey(t *testing.T) {
	_, err := Resolve("ghcr.io", KeyOverrides{}, []Source{fileSource("REGISTRY_USER", "u")})
	if err == nil || IsNoCredentials(err) || !strings.Contains(err.Error(), "no password") {
		t.Fatalf("err = %v, want a missing-password error", err)
	}
	_, err = Resolve("ghcr.io", KeyOverrides{}, []Source{fileSource("REGISTRY_TOKEN", "t")})
	if err == nil || IsNoCredentials(err) || !strings.Contains(err.Error(), "no user") {
		t.Fatalf("err = %v, want a missing-user error", err)
	}
}

func TestResolveIgnoresEmptyValues(t *testing.T) {
	src := fileSource("REGISTRY_USER", "", "DOCKER_USER", "real", "REGISTRY_TOKEN", "t")
	cred, err := Resolve("", KeyOverrides{}, []Source{src})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.User != "real" {
		t.Fatalf("user = %q, want an empty value skipped", cred.User)
	}
}

func TestCredentialsNeverMarshalThePassword(t *testing.T) {
	out, err := json.Marshal(&Credentials{User: "u", Password: "s3cr3t-token", Registry: "r"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(out), "s3cr3t") {
		t.Fatalf("marshaled Credentials leaked the password: %s", out)
	}
}

func TestSourceFromEnviron(t *testing.T) {
	s := SourceFromEnviron([]string{"A=1", "B=x=y", "=bad", "C"})
	if s.Name != EnvironmentSourceName || s.Values["A"] != "1" || s.Values["B"] != "x=y" || len(s.Values) != 2 {
		t.Fatalf("got %+v", s)
	}
}

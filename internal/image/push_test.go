package image

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func credSources() []Source {
	return []Source{fileSource("REGISTRY_USER", "alice", "REGISTRY_TOKEN", "s3cr3t-token")}
}

func TestPushLogsInOncePerRegistry(t *testing.T) {
	c, f := testClient(ToolPodman)
	f.digest = "sha256:" + strings.Repeat("a", 64)
	res, err := c.Push(context.Background(), PushOptions{
		Refs:    []string{"ghcr.io/o/a:dev", "ghcr.io/o/a:latest", "docker.io/o/a:dev"},
		Login:   true,
		Sources: credSources(),
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	var logins, pushes int
	for _, line := range f.argv() {
		switch {
		case strings.HasPrefix(line, "login "):
			logins++
			if strings.Contains(line, "s3cr3t") {
				t.Fatalf("password in argv: %s", line)
			}
		case strings.HasPrefix(line, "push --digestfile "):
			pushes++
		default:
			t.Fatalf("unexpected call: %s", line)
		}
	}
	if logins != 2 || pushes != 3 {
		t.Fatalf("logins = %d, pushes = %d; argv = %v", logins, pushes, f.argv())
	}
	if len(res.Logins) != 2 || res.Logins[0].Registry != "ghcr.io" || res.Logins[1].Registry != "docker.io" {
		t.Fatalf("logins = %+v", res.Logins)
	}
	if len(res.Pushed) != 3 || res.Pushed[0].Digest != f.digest || res.Pushed[2].Registry != "docker.io" {
		t.Fatalf("pushed = %+v", res.Pushed)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "s3cr3t") {
		t.Fatalf("JSON leaks the password: %s", raw)
	}
}

// The tool may already hold a session; missing credentials are not an error here.
func TestPushWithoutCredentialsSkipsLogin(t *testing.T) {
	c, f := testClient(ToolPodman)
	res, err := c.Push(context.Background(), PushOptions{
		Refs:    []string{"ghcr.io/o/a:dev"},
		Login:   true,
		Sources: []Source{fileSource(), envSource()},
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(res.Logins) != 0 || len(f.calls) != 1 || !strings.HasPrefix(f.argv()[0], "push ") {
		t.Fatalf("argv = %v, logins = %+v", f.argv(), res.Logins)
	}
	if res.Pushed[0].Digest != "" {
		t.Fatalf("digest = %q, want empty when podman wrote no digestfile", res.Pushed[0].Digest)
	}
}

func TestPushPartialCredentialsFail(t *testing.T) {
	c, f := testClient(ToolPodman)
	_, err := c.Push(context.Background(), PushOptions{
		Refs:    []string{"ghcr.io/o/a:dev"},
		Login:   true,
		Sources: []Source{fileSource("REGISTRY_USER", "alice")},
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no password") {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("nothing must run: %v", f.argv())
	}
}

func TestPushNoLoginNeverReadsCredentials(t *testing.T) {
	c, f := testClient(ToolDocker)
	f.out["push"] = []byte("dev: digest: sha256:" + strings.Repeat("b", 64) + " size: 1234\n")
	res, err := c.Push(context.Background(), PushOptions{
		Refs:    []string{"ghcr.io/o/a:dev"},
		Login:   false,
		Sources: credSources(),
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := f.argv(); len(got) != 1 || got[0] != "push ghcr.io/o/a:dev" {
		t.Fatalf("argv = %v", got)
	}
	if res.Pushed[0].Digest != "sha256:"+strings.Repeat("b", 64) {
		t.Fatalf("digest = %q", res.Pushed[0].Digest)
	}
}

func TestPushPodmanLoopbackDisablesTLSVerify(t *testing.T) {
	c, f := testClient(ToolPodman)
	if _, err := c.Push(context.Background(), PushOptions{Refs: []string{"localhost:5000/a:1"}}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	got := f.argv()[0]
	if !strings.HasPrefix(got, "push --digestfile ") || !strings.HasSuffix(got, " --tls-verify=false localhost:5000/a:1") {
		t.Fatalf("argv = %q", got)
	}
}

func TestPushDockerLoopbackKeepsArgvPlain(t *testing.T) {
	c, f := testClient(ToolDocker)
	if _, err := c.Push(context.Background(), PushOptions{Refs: []string{"localhost:5000/a:1"}}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := f.argv()[0]; got != "push localhost:5000/a:1" {
		t.Fatalf("argv = %q", got)
	}
}

func TestPushStopsAtFirstFailure(t *testing.T) {
	c, f := testClient(ToolDocker)
	f.err["push ghcr.io/o/a:dev"] = errors.New("denied")
	_, err := c.Push(context.Background(), PushOptions{
		Refs:  []string{"ghcr.io/o/a:dev", "ghcr.io/o/a:latest"},
		Login: false,
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(f.calls) != 1 {
		t.Fatalf("argv = %v, want the second push skipped", f.argv())
	}
}

func TestPushRejectsInvalidReferenceBeforeRunning(t *testing.T) {
	c, f := testClient(ToolDocker)
	if _, err := c.Push(context.Background(), PushOptions{Refs: []string{"Bad Ref"}}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error")
	}
	if len(f.calls) != 0 {
		t.Fatal("nothing must run")
	}
}

func TestPushNeedsAReference(t *testing.T) {
	c, _ := testClient(ToolDocker)
	if _, err := c.Push(context.Background(), PushOptions{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseDockerDigest(t *testing.T) {
	d := "sha256:" + strings.Repeat("c", 64)
	if got := parseDockerDigest("latest: digest: " + d + " size: 528\n"); got != d {
		t.Fatalf("got %q", got)
	}
	if got := parseDockerDigest("no digest here"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

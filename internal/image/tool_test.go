package image

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// fakeRunner records every call and answers from canned output keyed by the
// longest matching argv prefix.
type fakeRunner struct {
	kind   Tool
	out    map[string][]byte
	err    map[string]error
	digest string // podman: written to the --digestfile path on push
	calls  []call
}

type call struct {
	args  []string
	stdin []byte
}

func (f *fakeRunner) tool() Tool { return f.kind }

func (f *fakeRunner) lookup(args []string) ([]byte, error) {
	key := strings.Join(args, " ")
	var bestOut []byte
	var bestErr error
	best := -1
	for prefix, out := range f.out {
		if strings.HasPrefix(key, prefix) && len(prefix) > best {
			best, bestOut, bestErr = len(prefix), out, nil
		}
	}
	for prefix, err := range f.err {
		if strings.HasPrefix(key, prefix) && len(prefix) > best {
			best, bestOut, bestErr = len(prefix), nil, err
		}
	}
	return bestOut, bestErr
}

func (f *fakeRunner) run(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{args: args, stdin: stdin})
	return f.lookup(args)
}

func (f *fakeRunner) stream(_ context.Context, stdout, _ io.Writer, args ...string) error {
	f.calls = append(f.calls, call{args: args})
	for i, a := range args {
		if a == "--digestfile" && i+1 < len(args) && f.digest != "" {
			if err := os.WriteFile(args[i+1], []byte(f.digest+"\n"), 0o600); err != nil {
				return err
			}
		}
	}
	out, err := f.lookup(args)
	if out != nil {
		_, _ = stdout.Write(out)
	}
	return err
}

func (f *fakeRunner) argv() []string {
	lines := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		lines = append(lines, strings.Join(c.args, " "))
	}
	return lines
}

// fakeDeps wires lookPath and runners: present lists the installed binaries,
// broken the tools whose `info` fails.
func fakeDeps(present []string, broken map[Tool]error) (deps, map[Tool]*fakeRunner) {
	runners := map[Tool]*fakeRunner{}
	return deps{
		lookPath: func(file string) (string, error) {
			for _, p := range present {
				if p == file {
					return "/usr/bin/" + file, nil
				}
			}
			return "", errors.New("not found")
		},
		newRunner: func(tool Tool, path string) runner {
			r := &fakeRunner{kind: tool, out: map[string][]byte{}, err: map[string]error{}}
			if err, ok := broken[tool]; ok {
				r.err["info"] = err
			}
			runners[tool] = r
			return r
		},
	}, runners
}

// testClient builds a Client around a fake runner for the build/push/login tests.
func testClient(kind Tool) (*Client, *fakeRunner) {
	f := &fakeRunner{kind: kind, out: map[string][]byte{}, err: map[string]error{}}
	return &Client{r: f}, f
}

func TestParseTool(t *testing.T) {
	for _, tc := range []struct {
		flag, env string
		want      Tool
	}{
		{"", "", ToolAuto},
		{"auto", "", ToolAuto},
		{"podman", "", ToolPodman},
		{"docker", "", ToolDocker},
		{"", "podman", ToolPodman},
		{"docker", "podman", ToolDocker},
		{" PODMAN ", "", ToolPodman},
	} {
		got, err := ParseTool(tc.flag, tc.env)
		if err != nil {
			t.Fatalf("ParseTool(%q, %q): %v", tc.flag, tc.env, err)
		}
		if got != tc.want {
			t.Fatalf("ParseTool(%q, %q) = %q, want %q", tc.flag, tc.env, got, tc.want)
		}
	}
	if _, err := ParseTool("buildah", ""); err == nil {
		t.Fatal("expected an error for an unknown tool")
	}
}

func TestKnownListsAutoDetectionOrder(t *testing.T) {
	if got := strings.Join(Known(), ","); got != "podman,docker" {
		t.Fatalf("Known() = %q", got)
	}
}

func TestAutoPrefersPodman(t *testing.T) {
	d, _ := fakeDeps([]string{"podman", "docker"}, nil)
	c, err := newClient(context.Background(), Options{Tool: ToolAuto}, d)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c.Tool() != ToolPodman {
		t.Fatalf("tool = %q, want podman", c.Tool())
	}
}

// A Podman client whose machine is stopped must not strand a laptop that has Docker.
func TestAutoFallsBackToDockerWhenPodmanDoesNotAnswer(t *testing.T) {
	d, _ := fakeDeps([]string{"podman", "docker"}, map[Tool]error{ToolPodman: errors.New("Cannot connect to Podman")})
	c, err := newClient(context.Background(), Options{Tool: ToolAuto}, d)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c.Tool() != ToolDocker {
		t.Fatalf("tool = %q, want docker", c.Tool())
	}
}

func TestAutoUsesDockerWhenPodmanIsAbsent(t *testing.T) {
	d, _ := fakeDeps([]string{"docker"}, nil)
	c, err := newClient(context.Background(), Options{Tool: ToolAuto}, d)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c.Tool() != ToolDocker {
		t.Fatalf("tool = %q, want docker", c.Tool())
	}
}

func TestAutoReportsBothProblems(t *testing.T) {
	d, _ := fakeDeps([]string{"docker"}, map[Tool]error{ToolDocker: errors.New("daemon down")})
	_, err := newClient(context.Background(), Options{Tool: ToolAuto}, d)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"podman: podman not found in PATH", "docker: docker not reachable", "--tool", reachHint(ToolDocker)} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "install podman or docker") {
		t.Errorf("error %q should not tell the user to install a tool that is present but unreachable", msg)
	}
}

// A stopped Podman machine with no Docker at all must not tell the user to
// install Podman: it is installed, just not answering.
func TestAutoInstalledButStoppedPodmanKeepsItsHint(t *testing.T) {
	d, _ := fakeDeps([]string{"podman"}, map[Tool]error{ToolPodman: errors.New("Cannot connect to Podman")})
	_, err := newClient(context.Background(), Options{Tool: ToolAuto}, d)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{
		"podman: podman not reachable", "Cannot connect to Podman", reachHint(ToolPodman),
		"docker: docker not found in PATH",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "install podman or docker") {
		t.Errorf("error %q should not tell the user to install podman when it is installed", msg)
	}
}

func TestAutoNothingInstalledSaysInstall(t *testing.T) {
	d, _ := fakeDeps(nil, nil)
	_, err := newClient(context.Background(), Options{Tool: ToolAuto}, d)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "install podman or docker") {
		t.Fatalf("error %q should tell the user to install a tool", err.Error())
	}
}

func TestExplicitToolDoesNotFallBack(t *testing.T) {
	d, _ := fakeDeps([]string{"docker"}, nil)
	_, err := newClient(context.Background(), Options{Tool: ToolPodman}, d)
	if err == nil || !strings.Contains(err.Error(), "podman not found") {
		t.Fatalf("err = %v, want podman not found", err)
	}
}

func TestBinaryOverridesAreLookedUp(t *testing.T) {
	d, _ := fakeDeps([]string{"/opt/podman/bin/podman-remote"}, nil)
	c, err := newClient(context.Background(), Options{Tool: ToolPodman, Podman: "/opt/podman/bin/podman-remote"}, d)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c.Tool() != ToolPodman {
		t.Fatalf("tool = %q", c.Tool())
	}
}

func TestProbeRunsInfo(t *testing.T) {
	d, runners := fakeDeps([]string{"podman"}, nil)
	if _, err := newClient(context.Background(), Options{Tool: ToolPodman}, d); err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if got := runners[ToolPodman].argv(); len(got) != 1 || got[0] != "info" {
		t.Fatalf("argv = %v, want [info]", got)
	}
}

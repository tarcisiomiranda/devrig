//go:build integration

package image

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// liveClient skips unless podman or docker answers.
func liveClient(t *testing.T) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := New(ctx, Options{Tool: ToolAuto})
	if err != nil {
		t.Skipf("no image tool: %v", err)
	}
	return c
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry never listened on %d", port)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestImageLifecycle builds a one-line image and pushes it to a throwaway
// local registry started through the same tool. It is the smoke test
// AGENTS.md asks to keep passing.
func TestImageLifecycle(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	port := freePort(t)
	name := fmt.Sprintf("devrig-it-registry-%d", port)
	if _, err := c.r.run(ctx, nil, "run", "-d", "--rm", "--name", name,
		"-p", fmt.Sprintf("127.0.0.1:%d:5000", port), "docker.io/library/registry:2"); err != nil {
		t.Fatalf("start registry: %v", err)
	}
	defer func() { _, _ = c.r.run(context.Background(), nil, "rm", "-f", name) }()
	waitForPort(t, port)

	dir := t.TempDir()
	file := "Containerfile"
	if c.Tool() == ToolDocker { // docker only looks for Dockerfile
		file = "Dockerfile"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("FROM docker.io/library/alpine:3.20\nRUN echo devrig > /hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := fmt.Sprintf("localhost:%d/devrig-smoke:%d", port, time.Now().Unix())
	defer func() { _, _ = c.r.run(context.Background(), nil, "image", "rm", "-f", ref) }()

	build, err := c.Build(ctx, BuildOptions{Context: dir, Tags: []string{ref}}, os.Stderr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.HasPrefix(build.ImageID, "sha256:") {
		t.Fatalf("image id = %q", build.ImageID)
	}

	push, err := c.Push(ctx, PushOptions{Refs: []string{ref}, Login: false}, os.Stderr)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(push.Pushed) != 1 || push.Pushed[0].Registry != fmt.Sprintf("localhost:%d", port) {
		t.Fatalf("pushed = %+v", push.Pushed)
	}
	if push.Pushed[0].Digest == "" {
		t.Fatalf("digest not captured for %s", c.Tool())
	}
}

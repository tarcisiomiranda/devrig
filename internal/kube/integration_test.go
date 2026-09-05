//go:build integration

package kube

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tarcisiomiranda/devrig/internal/engine"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

// liveClient skips the test unless kubectl can reach a cluster.
func liveClient(t *testing.T) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := New(ctx, Options{Namespace: os.Getenv("DEVRIG_TEST_NAMESPACE")})
	if err != nil {
		t.Skipf("no reachable cluster: %v", err)
	}
	return c
}

func TestKubeLifecycle(t *testing.T) {
	c := liveClient(t)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	name := "devrig-it"
	defer func() {
		if _, err := c.Down(context.Background(), name); err != nil {
			t.Logf("cleanup: %v", err)
		}
	}()

	st, err := c.Up(ctx, rig.UpOptions{Name: name, Engine: engine.Default()})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !st.Ready || st.Port == 0 || !strings.HasPrefix(st.URL, "postgres://") {
		t.Fatalf("status = %+v", st)
	}
	if st.Backend != "kube" || st.Host != "127.0.0.1" {
		t.Fatalf("the kube backend must publish on loopback: %+v", st)
	}

	again, err := c.Up(ctx, rig.UpOptions{Name: name, Engine: engine.Default()})
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if again.ContainerName != st.ContainerName || again.Port != st.Port {
		t.Fatalf("up must reuse a matching instance: %+v vs %+v", again, st)
	}

	url, err := c.URL(ctx, name)
	if err != nil || url != st.URL {
		t.Fatalf("URL = %q (%v), want %q", url, err, st.URL)
	}

	logs, err := c.Logs(ctx, name, 20)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if !strings.Contains(logs.Logs, "database system is ready") {
		t.Fatalf("logs do not look like postgres: %q", logs.Logs)
	}

	res, err := c.Down(ctx, name)
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	if !res.Removed {
		t.Fatalf("down = %+v", res)
	}
	if _, err := c.Status(ctx, name); !rig.IsNotFound(err) {
		t.Fatalf("status after down = %v", err)
	}
}

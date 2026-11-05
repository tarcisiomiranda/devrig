package kube

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run helper: %v", err)
	}
	return cmd.Process.Pid
}

func TestAwaitForwardPortReadsThePortKubectlPrinted(t *testing.T) {
	log := filepath.Join(t.TempDir(), "pf.log")
	if err := os.WriteFile(log, []byte("Forwarding from 127.0.0.1:34567 -> 5432\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	port, err := awaitForwardPort(context.Background(), log, os.Getpid(), time.Second)
	if err != nil {
		t.Fatalf("awaitForwardPort: %v", err)
	}
	if port != 34567 {
		t.Fatalf("port = %d, want 34567", port)
	}
}

func TestAwaitForwardPortReportsWhyKubectlDied(t *testing.T) {
	log := filepath.Join(t.TempDir(), "pf.log")
	if err := os.WriteFile(log, []byte("error: unable to forward port because pod is not running\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := awaitForwardPort(context.Background(), log, deadPID(t), time.Second)
	if err == nil {
		t.Fatal("expected an error when kubectl exits before publishing a port")
	}
	if !strings.Contains(err.Error(), "pod is not running") {
		t.Fatalf("error should carry kubectl's own output: %v", err)
	}
}

func TestForwardStateIsScopedByContextAndNamespace(t *testing.T) {
	t.Setenv("DEVRIG_STATE_DIR", t.TempDir())
	dev := &Client{namespace: "dev", contextName: "k3s"}
	prd := &Client{namespace: "prd", contextName: "k3s"}
	other := &Client{namespace: "dev", contextName: "gke"}

	if dev.forwardPath("dev", "app") == prd.forwardPath("prd", "app") {
		t.Fatal("the same instance name in two namespaces must not share a state file")
	}
	if dev.forwardPath("dev", "app") == other.forwardPath("dev", "app") {
		t.Fatal("the same instance name in two clusters must not share a state file")
	}
}

func TestForwardKeyIsFilesystemSafe(t *testing.T) {
	c := &Client{namespace: "dev", contextName: "gke_my-project/us-central1/cluster"}
	if strings.ContainsAny(c.forwardKey("dev", "app"), "/\\ ") {
		t.Fatalf("key = %q", c.forwardKey("dev", "app"))
	}
}

func TestLiveForwardIgnoresADeadProcess(t *testing.T) {
	t.Setenv("DEVRIG_STATE_DIR", t.TempDir())
	c := &Client{namespace: "dev", contextName: "k3s"}
	f := &forward{
		Name: "app", Namespace: "dev", Context: "k3s", Pod: "devrig-app",
		LocalPort: 34567, ContainerPort: 5432, PID: deadPID(t),
	}
	if err := c.saveForward(f); err != nil {
		t.Fatalf("saveForward: %v", err)
	}
	if got := c.liveForward("dev", "app", "devrig-app", 5432, 0); got != nil {
		t.Fatal("a recorded forward whose process is gone must not be reused")
	}
}

func TestLiveForwardIgnoresADifferentPod(t *testing.T) {
	t.Setenv("DEVRIG_STATE_DIR", t.TempDir())
	c := &Client{namespace: "dev", contextName: "k3s"}
	f := &forward{Name: "app", Namespace: "dev", Pod: "devrig-old", LocalPort: 1, ContainerPort: 5432, PID: os.Getpid()}
	if err := c.saveForward(f); err != nil {
		t.Fatal(err)
	}
	if got := c.liveForward("dev", "app", "devrig-new", 5432, 0); got != nil {
		t.Fatal("a forward pointing at a replaced pod must not be reused")
	}
}

func TestStopForwardForgetsTheInstance(t *testing.T) {
	t.Setenv("DEVRIG_STATE_DIR", t.TempDir())
	c := &Client{namespace: "dev", contextName: "k3s"}
	f := &forward{Name: "app", Namespace: "dev", Pod: "devrig-app", LocalPort: 1, ContainerPort: 5432, PID: deadPID(t)}
	if err := c.saveForward(f); err != nil {
		t.Fatal(err)
	}
	c.stopForward("dev", "app")
	if _, err := os.Stat(c.forwardPath("dev", "app")); !os.IsNotExist(err) {
		t.Fatalf("state file survived stopForward: %v", err)
	}
}

func TestStateDirHonoursXDG(t *testing.T) {
	t.Setenv("DEVRIG_STATE_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	if got := StateDir(); got != filepath.Join("/tmp/xdg-state", "devrig") {
		t.Fatalf("StateDir = %q", got)
	}
}

package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// forward is the local half of an instance: a detached `kubectl port-forward`
// plus the loopback port it publishes. It outlives the devrig process that
// started it, so its pid and port are kept on disk.
type forward struct {
	Name          string    `json:"name"`
	Namespace     string    `json:"namespace"`
	Context       string    `json:"context"`
	Pod           string    `json:"pod"`
	LocalPort     int       `json:"local_port"`
	ContainerPort int       `json:"container_port"`
	PID           int       `json:"pid"`
	Log           string    `json:"log"`
	StartedAt     time.Time `json:"started_at"`
}

var forwardingRe = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+)`)

// StateDir is where devrig keeps port-forward bookkeeping.
func StateDir() string {
	if dir := os.Getenv("DEVRIG_STATE_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "devrig")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "devrig")
	}
	return filepath.Join(home, ".local", "state", "devrig")
}

func forwardDir() string { return filepath.Join(StateDir(), "forwards") }

func sanitizeKeyPart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "default"
	}
	return out
}

func (c *Client) forwardKey(namespace, name string) string {
	return sanitizeKeyPart(c.contextName) + "__" + sanitizeKeyPart(namespace) + "__" + sanitizeKeyPart(name)
}

func (c *Client) forwardPath(namespace, name string) string {
	return filepath.Join(forwardDir(), c.forwardKey(namespace, name)+".json")
}

func (c *Client) forwardLogPath(namespace, name string) string {
	return filepath.Join(forwardDir(), c.forwardKey(namespace, name)+".log")
}

func (c *Client) loadForward(namespace, name string) *forward {
	b, err := os.ReadFile(c.forwardPath(namespace, name))
	if err != nil {
		return nil
	}
	var f forward
	if err := json.Unmarshal(b, &f); err != nil {
		return nil
	}
	return &f
}

func (c *Client) saveForward(f *forward) error {
	if err := os.MkdirAll(forwardDir(), 0o755); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.forwardPath(f.Namespace, f.Name), b, 0o600)
}

// liveForward returns the recorded forward when its process is still running
// and the local port still accepts connections.
func (c *Client) liveForward(namespace, name, podName string, containerPort, wantPort int) *forward {
	f := c.loadForward(namespace, name)
	if f == nil {
		return nil
	}
	if f.Pod != podName || f.ContainerPort != containerPort {
		return nil
	}
	if wantPort > 0 && f.LocalPort != wantPort {
		return nil
	}
	if f.PID <= 0 || !processAlive(f.PID) {
		return nil
	}
	if !portAccepts(f.LocalPort) {
		return nil
	}
	return f
}

// ensureForward returns a working local port for the pod, starting a new
// port-forward when the recorded one is gone.
func (c *Client) ensureForward(ctx context.Context, namespace, name, podName string, containerPort, wantPort int) (*forward, error) {
	if f := c.liveForward(namespace, name, podName, containerPort, wantPort); f != nil {
		return f, nil
	}
	c.stopForward(namespace, name)
	return c.startForward(ctx, namespace, name, podName, containerPort, wantPort)
}

func (c *Client) startForward(ctx context.Context, namespace, name, podName string, containerPort, wantPort int) (*forward, error) {
	if err := os.MkdirAll(forwardDir(), 0o755); err != nil {
		return nil, fmt.Errorf("state dir: %w", err)
	}
	logPath := c.forwardLogPath(namespace, name)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("forward log: %w", err)
	}
	defer logFile.Close()

	mapping := fmt.Sprintf(":%d", containerPort)
	if wantPort > 0 {
		mapping = fmt.Sprintf("%d:%d", wantPort, containerPort)
	}
	args := append(c.kubectl.globalArgs(), namespaceArgs(namespace)...)
	args = append(args, "port-forward", "pod/"+podName, mapping, "--address", "127.0.0.1")

	cmd := exec.Command(c.kubectl.binary(), args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	setDetached(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("kubectl port-forward: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	port, err := awaitForwardPort(ctx, logPath, pid, 30*time.Second)
	if err != nil {
		_ = killGroup(pid)
		return nil, err
	}

	f := &forward{
		Name:          name,
		Namespace:     namespace,
		Context:       c.contextName,
		Pod:           podName,
		LocalPort:     port,
		ContainerPort: containerPort,
		PID:           pid,
		Log:           logPath,
		StartedAt:     time.Now(),
	}
	if err := c.saveForward(f); err != nil {
		_ = killGroup(pid)
		return nil, err
	}
	return f, nil
}

// awaitForwardPort reads the port kubectl chose out of its own output.
func awaitForwardPort(ctx context.Context, logPath string, pid int, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		b, _ := os.ReadFile(logPath)
		if m := forwardingRe.FindSubmatch(b); m != nil {
			port, err := strconv.Atoi(string(m[1]))
			if err == nil && port > 0 {
				return port, nil
			}
		}
		if !processAlive(pid) {
			return 0, fmt.Errorf("kubectl port-forward exited before publishing a port%s", detail(tail(string(b), 10)))
		}
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("kubectl port-forward did not publish a port: %w%s", err, detail(tail(string(b), 10)))
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("kubectl port-forward did not publish a port within %s%s", timeout, detail(tail(string(b), 10)))
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// stopForward kills the recorded forward and forgets it. Missing is success.
func (c *Client) stopForward(namespace, name string) {
	f := c.loadForward(namespace, name)
	if f != nil && f.PID > 0 && processAlive(f.PID) {
		_ = killGroup(f.PID)
	}
	_ = os.Remove(c.forwardPath(namespace, name))
	if f != nil && f.Log != "" {
		_ = os.Remove(f.Log)
	}
}

func portAccepts(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func tail(s string, lines int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

// Package kube runs devrig instances as Kubernetes pods, driving the cluster
// through the kubectl binary already configured on the machine.
package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultBinary is the kubectl executable looked up in PATH.
const DefaultBinary = "kubectl"

// Options configures how devrig talks to a cluster.
type Options struct {
	Binary        string
	Context       string
	Namespace     string
	Kubeconfig    string
	AllNamespaces bool
	CPU           string
	Memory        string
}

type runner interface {
	run(ctx context.Context, stdin []byte, args ...string) ([]byte, error)
	binary() string
	globalArgs() []string
}

type execRunner struct {
	bin  string
	base []string
}

func (r *execRunner) binary() string { return r.bin }

func (r *execRunner) globalArgs() []string { return append([]string(nil), r.base...) }

func (r *execRunner) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	full := append(r.globalArgs(), args...)
	cmd := exec.CommandContext(ctx, r.bin, full...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return out.Bytes(), fmt.Errorf("kubectl %s: %w%s", strings.Join(args, " "), err, detail(msg))
	}
	return out.Bytes(), nil
}

func detail(msg string) string {
	if msg == "" {
		return ""
	}
	return "\n" + msg
}

func newExecRunner(opt Options) (*execRunner, error) {
	bin := opt.Binary
	if bin == "" {
		bin = DefaultBinary
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("%s not found in PATH: %w\nhint: the kube backend drives the cluster through kubectl; install it or pass --backend docker", bin, err)
	}
	var base []string
	if opt.Kubeconfig != "" {
		base = append(base, "--kubeconfig", opt.Kubeconfig)
	}
	if opt.Context != "" {
		base = append(base, "--context", opt.Context)
	}
	return &execRunner{bin: path, base: base}, nil
}

type versionOutput struct {
	ServerVersion *struct {
		GitVersion string `json:"gitVersion"`
	} `json:"serverVersion"`
}

// probeCluster fails when kubectl cannot reach the API server.
func probeCluster(ctx context.Context, r runner) (string, error) {
	out, err := r.run(ctx, nil, "version", "-o", "json")
	if err != nil {
		return "", fmt.Errorf("kubernetes not reachable: %w\nhint: check `kubectl config current-context` and that the cluster answers (`kubectl get ns`)", err)
	}
	var v versionOutput
	if err := json.Unmarshal(out, &v); err != nil || v.ServerVersion == nil {
		return "", fmt.Errorf("kubernetes not reachable: kubectl reported no server version\nhint: check `kubectl config current-context` and that the cluster answers (`kubectl get ns`)")
	}
	return v.ServerVersion.GitVersion, nil
}

func currentContext(ctx context.Context, r runner) string {
	out, err := r.run(ctx, nil, "config", "current-context")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func currentNamespace(ctx context.Context, r runner) string {
	out, err := r.run(ctx, nil, "config", "view", "--minify", "-o", "jsonpath={..namespace}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

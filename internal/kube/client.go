package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tarcisiomiranda/devrig/internal/engine"
	"github.com/tarcisiomiranda/devrig/internal/instance"
	"github.com/tarcisiomiranda/devrig/internal/rig"
	"github.com/tarcisiomiranda/devrig/internal/wait"
)

// DefaultTimeout is the readiness budget for a pod: image pulls on a cold node
// take far longer than a local container start.
const DefaultTimeout = 180 * time.Second

// Client runs devrig instances as pods on a Kubernetes cluster.
type Client struct {
	kubectl       runner
	namespace     string
	contextName   string
	serverVersion string
	allNamespaces bool
	cpu           string
	memory        string
}

// New verifies kubectl can reach a cluster and resolves context and namespace.
func New(ctx context.Context, opt Options) (*Client, error) {
	r, err := newExecRunner(opt)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	version, err := probeCluster(pctx, r)
	if err != nil {
		return nil, err
	}
	namespace := opt.Namespace
	if namespace == "" {
		namespace = currentNamespace(pctx, r)
	}
	if namespace == "" {
		namespace = "default"
	}
	contextName := opt.Context
	if contextName == "" {
		contextName = currentContext(pctx, r)
	}
	return &Client{
		kubectl:       r,
		namespace:     namespace,
		contextName:   contextName,
		serverVersion: version,
		allNamespaces: opt.AllNamespaces,
		cpu:           opt.CPU,
		memory:        opt.Memory,
	}, nil
}

// Kind reports which backend this client drives.
func (c *Client) Kind() rig.Kind { return rig.KindKube }

// Close releases nothing: kubectl is a child process, not a connection.
func (c *Client) Close() error { return nil }

// Namespace is the namespace this client operates in.
func (c *Client) Namespace() string { return c.namespace }

// Context is the kubeconfig context this client operates in.
func (c *Client) Context() string { return c.contextName }

// ServerVersion is the API server version kubectl reported.
func (c *Client) ServerVersion() string { return c.serverVersion }

func namespaceArgs(namespace string) []string {
	if namespace == "" {
		return nil
	}
	return []string{"-n", namespace}
}

func (c *Client) namespaceArgs() []string { return namespaceArgs(c.namespace) }

func (c *Client) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	return c.kubectl.run(ctx, stdin, append(c.namespaceArgs(), args...)...)
}

// Up creates or reuses a ready instance and a local port-forward to it.
func (c *Client) Up(ctx context.Context, opt rig.UpOptions) (*rig.Status, error) {
	if err := instance.ValidateName(opt.Name); err != nil {
		return nil, err
	}
	if opt.Engine == nil {
		opt.Engine = engine.Default()
	}
	if opt.User == "" {
		opt.User = opt.Engine.DefaultUser
	}
	if opt.Password == "" {
		opt.Password = opt.Engine.DefaultPassword
	}
	if opt.Database == "" {
		opt.Database = instance.DefaultDatabase(opt.Name)
	}
	if opt.Image == "" {
		opt.Image = opt.Engine.DefaultImage
	}
	if opt.ContainerPort <= 0 {
		opt.ContainerPort = opt.Engine.ContainerPort
	}
	if opt.Timeout <= 0 {
		opt.Timeout = DefaultTimeout
	}

	podName := instance.PodName(opt.Name)

	existing, err := c.findPod(ctx, opt.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.running() && c.matchesConfig(existing, opt) {
			st, err := c.readyStatus(ctx, existing, opt.Port, reuseTimeout(opt.Timeout))
			if err == nil {
				return st, nil
			}
		}
		if err := c.deletePod(ctx, existing.Metadata.Name); err != nil {
			return nil, err
		}
		c.stopForward(c.namespace, opt.Name)
	}

	body, err := manifest(podSpec{opt: opt, namespace: c.namespace, cpu: c.cpu, memory: c.memory})
	if err != nil {
		return nil, err
	}
	if _, err := c.run(ctx, body, "apply", "-f", "-"); err != nil {
		return nil, fmt.Errorf("create pod %s: %w", podName, err)
	}

	wctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	p, err := c.waitRunning(wctx, podName, opt.Timeout)
	if err != nil {
		c.stopForward(c.namespace, opt.Name)
		_ = c.deletePod(context.Background(), podName)
		return nil, containerPortHint(err, opt)
	}

	st, err := c.readyStatus(wctx, p, opt.Port, opt.Timeout)
	if err != nil {
		c.stopForward(c.namespace, opt.Name)
		_ = c.deletePod(context.Background(), podName)
		return nil, containerPortHint(err, opt)
	}
	return st, nil
}

// reuseTimeout bounds the probe of an existing pod: a stale one must be
// replaced quickly instead of burning the whole up budget.
func reuseTimeout(timeout time.Duration) time.Duration {
	const max = 30 * time.Second
	if timeout > 0 && timeout < max {
		return timeout
	}
	return max
}

// containerPortHint names the likeliest cause when an overridden container port never answers.
func containerPortHint(err error, opt rig.UpOptions) error {
	if err == nil || opt.Engine == nil || opt.ContainerPort == opt.Engine.ContainerPort {
		return err
	}
	return fmt.Errorf("%w\nhint: --container-port %d requires an image that listens there; %s listens on %d by default",
		err, opt.ContainerPort, opt.Image, opt.Engine.ContainerPort)
}

// readyStatus forwards a local port to the pod and waits until it answers
// queries, restarting the forward when kubectl drops it.
func (c *Client) readyStatus(ctx context.Context, p *pod, wantPort int, timeout time.Duration) (*rig.Status, error) {
	st := c.statusFromPod(p, 0)
	spec := c.specOf(p)
	namespace := p.Metadata.Namespace

	rctx := ctx
	if _, ok := ctx.Deadline(); !ok {
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var lastErr error
	for {
		if err := rctx.Err(); err != nil {
			return nil, forwardFailure(p.Metadata.Name, lastErr, err)
		}
		f, err := c.ensureForward(rctx, namespace, st.Name, p.Metadata.Name, st.ContainerPort, wantPort)
		if err != nil {
			lastErr = err
			if !sleepCtx(rctx, 500*time.Millisecond) {
				return nil, forwardFailure(p.Metadata.Name, lastErr, rctx.Err())
			}
			continue
		}
		url := instance.URL(spec, st.User, st.Password, instance.DefaultHost, f.LocalPort, st.Database)

		attempt, cancel := context.WithTimeout(rctx, 5*time.Second)
		err = wait.Ready(attempt, spec, url, instance.DefaultHost, f.LocalPort)
		cancel()
		if err == nil {
			st.Port = f.LocalPort
			st.URL = url
			st.Ready = true
			return st, nil
		}
		lastErr = err
		// kubectl exits when the pod refuses the connection, which happens while
		// the server is still starting; drop the dead tunnel and open a new one.
		if !processAlive(f.PID) || !portAccepts(f.LocalPort) {
			c.stopForward(namespace, st.Name)
		}
	}
}

func forwardFailure(podName string, lastErr, ctxErr error) error {
	if lastErr == nil {
		return fmt.Errorf("pod %s never answered queries: %w", podName, ctxErr)
	}
	return fmt.Errorf("pod %s never answered queries through the port-forward: %w", podName, lastErr)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (c *Client) matchesConfig(p *pod, opt rig.UpOptions) bool {
	return p.annotation(instance.LabelUser) == opt.User &&
		p.annotation(instance.LabelPass) == opt.Password &&
		p.annotation(instance.LabelDB) == opt.Database &&
		p.image() == opt.Image &&
		p.containerPort(0) == opt.ContainerPort &&
		p.Metadata.Labels[instance.LabelEngine] == opt.Engine.Name.String()
}

// waitRunning blocks until the pod is Running with every container ready.
func (c *Client) waitRunning(ctx context.Context, podName string, timeout time.Duration) (*pod, error) {
	var last *pod
	for {
		p, err := c.getPod(ctx, podName)
		if err != nil {
			return nil, err
		}
		if p != nil {
			last = p
			if p.running() {
				return p, nil
			}
			if p.Status.Phase == "Failed" {
				return nil, fmt.Errorf("pod %s failed%s", podName, detail(p.blocked()))
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("pod %s did not start within %s%s", podName, timeout, detail(describeStuck(last)))
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func describeStuck(p *pod) string {
	if p == nil {
		return "the pod was never created; check quotas and RBAC (`kubectl auth can-i create pods`)"
	}
	msg := "phase=" + p.Status.Phase
	if reason := p.blocked(); reason != "" {
		msg += ", " + reason
	} else if p.Status.Phase == "Running" {
		msg += ", the readiness probe never saw the port answer inside the pod"
	}
	return msg
}

// Down removes the pod and the local forward. Missing is success.
func (c *Client) Down(ctx context.Context, name string) (*rig.DownResult, error) {
	if err := instance.ValidateName(name); err != nil {
		return nil, err
	}
	existing, err := c.findPod(ctx, name)
	if err != nil {
		return nil, err
	}
	c.stopForward(c.namespace, name)
	if existing == nil {
		return &rig.DownResult{Name: name, Backend: rig.KindKube.String(), Removed: false, State: "absent"}, nil
	}
	c.stopForward(existing.Metadata.Namespace, name)
	if err := c.deletePod(ctx, existing.Metadata.Name); err != nil {
		return nil, err
	}
	return &rig.DownResult{Name: name, Backend: rig.KindKube.String(), Removed: true, State: "absent"}, nil
}

// Status reports the pod plus the state of its local forward.
func (c *Client) Status(ctx context.Context, name string) (*rig.Status, error) {
	if err := instance.ValidateName(name); err != nil {
		return nil, err
	}
	p, err := c.findPod(ctx, name)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, &rig.NotFoundError{Name: name, Where: "namespace " + c.namespace}
	}
	return c.probedStatus(ctx, p), nil
}

// List returns every managed pod in the namespace (or in the cluster with --all-namespaces).
func (c *Client) List(ctx context.Context) ([]rig.Status, error) {
	args := []string{"-l", instance.LabelManaged + "=1"}
	if c.allNamespaces {
		args = append(args, "--all-namespaces")
	} else {
		args = append(namespaceArgs(c.namespace), args...)
	}
	pods, err := c.getPods(ctx, args...)
	if err != nil {
		return nil, err
	}
	out := make([]rig.Status, 0, len(pods))
	for i := range pods {
		out = append(out, *c.probedStatus(ctx, &pods[i]))
	}
	return out, nil
}

// URL re-establishes the forward if needed, so the printed DSN is connectable.
func (c *Client) URL(ctx context.Context, name string) (string, error) {
	if err := instance.ValidateName(name); err != nil {
		return "", err
	}
	p, err := c.findPod(ctx, name)
	if err != nil {
		return "", err
	}
	if p == nil {
		return "", &rig.NotFoundError{Name: name, Where: "namespace " + c.namespace}
	}
	if !p.running() {
		return "", fmt.Errorf("instance %q is not ready (state=%s)%s", name, p.state(), detail(p.blocked()))
	}
	st, err := c.readyStatus(ctx, p, 0, 30*time.Second)
	if err != nil {
		return "", err
	}
	return st.URL, nil
}

// Logs returns the container logs of the pod.
func (c *Client) Logs(ctx context.Context, name string, tailLines int) (*rig.LogsResult, error) {
	if err := instance.ValidateName(name); err != nil {
		return nil, err
	}
	p, err := c.findPod(ctx, name)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, &rig.NotFoundError{Name: name, Where: "namespace " + c.namespace}
	}
	if tailLines <= 0 {
		tailLines = 100
	}
	args := append(namespaceArgs(p.Metadata.Namespace),
		"logs", "pod/"+p.Metadata.Name,
		"-c", p.containerName(c.specOf(p).Name.String()),
		"--tail="+strconv.Itoa(tailLines))
	out, err := c.kubectl.run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	return &rig.LogsResult{Name: name, Backend: rig.KindKube.String(), Logs: string(out)}, nil
}

func (c *Client) specOf(p *pod) *engine.Spec {
	if name := p.Metadata.Labels[instance.LabelEngine]; name != "" {
		if spec, err := engine.Lookup(name); err == nil {
			return spec
		}
	}
	return engine.Default()
}

// statusFromPod builds the JSON shape from pod metadata alone.
func (c *Client) statusFromPod(p *pod, localPort int) *rig.Status {
	spec := c.specOf(p)
	name := p.instanceName()
	user := p.annotation(instance.LabelUser)
	if user == "" {
		user = spec.DefaultUser
	}
	pass := p.annotation(instance.LabelPass)
	if pass == "" {
		pass = spec.DefaultPassword
	}
	db := p.annotation(instance.LabelDB)
	if db == "" {
		db = instance.DefaultDatabase(name)
	}
	st := &rig.Status{
		Name:          name,
		Backend:       rig.KindKube.String(),
		Engine:        spec.Name.String(),
		Namespace:     p.Metadata.Namespace,
		Context:       c.contextName,
		ContainerName: p.Metadata.Name,
		State:         p.state(),
		Host:          instance.DefaultHost,
		ContainerPort: p.containerPort(spec.ContainerPort),
		User:          user,
		Password:      pass,
		Database:      db,
		Image:         p.image(),
	}
	if localPort > 0 {
		st.Port = localPort
		st.URL = instance.URL(spec, user, pass, instance.DefaultHost, localPort, db)
	}
	return st
}

// probedStatus reports readiness through an existing forward, never starting one.
func (c *Client) probedStatus(ctx context.Context, p *pod) *rig.Status {
	st := c.statusFromPod(p, 0)
	f := c.liveForward(p.Metadata.Namespace, st.Name, p.Metadata.Name, st.ContainerPort, 0)
	if f == nil {
		return st
	}
	st.Port = f.LocalPort
	st.URL = instance.URL(c.specOf(p), st.User, st.Password, instance.DefaultHost, f.LocalPort, st.Database)
	if !p.running() {
		return st
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := wait.Ready(pctx, c.specOf(p), st.URL, instance.DefaultHost, f.LocalPort); err == nil {
		st.Ready = true
	}
	return st
}

func (c *Client) findPod(ctx context.Context, name string) (*pod, error) {
	selector := instance.LabelManaged + "=1," + instance.LabelName + "=" + name
	pods, err := c.getPods(ctx, append(c.namespaceArgs(), "-l", selector)...)
	if err != nil {
		return nil, err
	}
	for i := range pods {
		if pods[i].running() {
			return &pods[i], nil
		}
	}
	if len(pods) > 0 {
		return &pods[0], nil
	}
	return nil, nil
}

func (c *Client) getPod(ctx context.Context, podName string) (*pod, error) {
	out, err := c.run(ctx, nil, "get", "pod", podName, "-o", "json", "--ignore-not-found")
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	var p pod
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("parse kubectl get pod: %w", err)
	}
	return &p, nil
}

func (c *Client) deletePod(ctx context.Context, podName string) error {
	if _, err := c.run(ctx, nil, "delete", "pod", podName, "--ignore-not-found", "--now"); err != nil {
		return fmt.Errorf("delete pod %s: %w", podName, err)
	}
	return nil
}

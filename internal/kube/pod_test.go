package kube

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tarcisiomiranda/devrig/internal/instance"
)

const runningPodJSON = `{
  "metadata": {
    "name": "devrig-app", "namespace": "dev",
    "labels": {"devrig.managed": "1", "devrig.name": "app", "devrig.engine": "postgres"},
    "annotations": {"devrig.user": "test", "devrig.password": "pw", "devrig.db": "app_test",
                    "devrig.image": "postgres:16-alpine", "devrig.container_port": "5432"}
  },
  "spec": {"containers": [{"name": "postgres", "image": "postgres:16-alpine",
                           "ports": [{"containerPort": 5432}]}]},
  "status": {"phase": "Running", "containerStatuses": [{"ready": true, "restartCount": 0}]}
}`

const pendingPodJSON = `{
  "metadata": {"name": "devrig-app", "namespace": "dev"},
  "spec": {"containers": [{"name": "postgres", "image": "nope:1", "ports": [{"containerPort": 5432}]}]},
  "status": {"phase": "Pending", "containerStatuses": [
    {"ready": false, "state": {"waiting": {"reason": "ImagePullBackOff", "message": "back-off pulling image"}}}]}
}`

func parsePod(t *testing.T, raw string) *pod {
	t.Helper()
	var p pod
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &p
}

func TestPodReadsIdentityFromLabelsAndAnnotations(t *testing.T) {
	p := parsePod(t, runningPodJSON)
	if p.instanceName() != "app" {
		t.Fatalf("instance name = %q", p.instanceName())
	}
	if p.annotation(instance.LabelDB) != "app_test" {
		t.Fatalf("database = %q", p.annotation(instance.LabelDB))
	}
	if p.containerPort(0) != 5432 || p.image() != "postgres:16-alpine" {
		t.Fatalf("port/image = %d %q", p.containerPort(0), p.image())
	}
	if !p.running() || p.state() != "running" {
		t.Fatalf("running = %v, state = %q", p.running(), p.state())
	}
}

// A pod whose container is not ready must not count as running: the port-forward
// would attach to a server that is not listening yet.
func TestPodNotRunningUntilContainersAreReady(t *testing.T) {
	p := parsePod(t, runningPodJSON)
	p.Status.ContainerStatuses[0].Ready = false
	if p.running() {
		t.Fatal("a pod with an unready container must not be running")
	}
}

func TestPodTerminatingIsNotRunning(t *testing.T) {
	p := parsePod(t, runningPodJSON)
	p.Metadata.DeletionTimestamp = "2026-09-05T12:00:00Z"
	if p.running() || p.state() != "terminating" {
		t.Fatalf("state = %q, running = %v", p.state(), p.running())
	}
}

func TestPodBlockedNamesTheWaitingReason(t *testing.T) {
	p := parsePod(t, pendingPodJSON)
	if got := p.blocked(); !strings.Contains(got, "ImagePullBackOff") {
		t.Fatalf("blocked() = %q, want the waiting reason", got)
	}
	if p.state() != "pending" {
		t.Fatalf("state = %q", p.state())
	}
}

func TestPodFallsBackToTheContainerPortWhenTheAnnotationIsGone(t *testing.T) {
	p := parsePod(t, runningPodJSON)
	delete(p.Metadata.Annotations, instance.LabelContainerPort)
	if got := p.containerPort(1234); got != 5432 {
		t.Fatalf("containerPort = %d, want the port declared by the container", got)
	}
}

type fakeRunner struct {
	out  map[string][]byte
	err  map[string]error
	seen [][]string
}

func (f *fakeRunner) binary() string       { return "kubectl" }
func (f *fakeRunner) globalArgs() []string { return nil }

func (f *fakeRunner) run(_ context.Context, _ []byte, args ...string) ([]byte, error) {
	f.seen = append(f.seen, args)
	key := strings.Join(args, " ")
	for prefix, err := range f.err {
		if strings.Contains(key, prefix) {
			return nil, err
		}
	}
	for prefix, out := range f.out {
		if strings.Contains(key, prefix) {
			return out, nil
		}
	}
	return []byte(""), nil
}

func testClient(t *testing.T, r runner) *Client {
	t.Helper()
	t.Setenv("DEVRIG_STATE_DIR", t.TempDir())
	return &Client{kubectl: r, namespace: "dev", contextName: "k3s-dev"}
}

func TestListReportsInstancesWithoutAForward(t *testing.T) {
	fake := &fakeRunner{out: map[string][]byte{
		"get pods": []byte(`{"items": [` + runningPodJSON + `]}`),
	}}
	c := testClient(t, fake)

	items, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("List returned %d items", len(items))
	}
	got := items[0]
	if got.Name != "app" || got.Backend != "kube" || got.Namespace != "dev" || got.Context != "k3s-dev" {
		t.Fatalf("status = %+v", got)
	}
	// No forward is recorded, so there is no local port and nothing is connectable.
	if got.Ready || got.Port != 0 || got.URL != "" {
		t.Fatalf("a pod without a live forward must not look connectable: %+v", got)
	}
}

func TestListSelectsOnlyManagedPods(t *testing.T) {
	fake := &fakeRunner{out: map[string][]byte{"get pods": []byte(`{"items": []}`)}}
	c := testClient(t, fake)
	if _, err := c.List(context.Background()); err != nil {
		t.Fatalf("List: %v", err)
	}
	joined := strings.Join(fake.seen[0], " ")
	if !strings.Contains(joined, "-l "+instance.LabelManaged+"=1") {
		t.Fatalf("List must filter by the managed label: %v", fake.seen[0])
	}
	if !strings.Contains(joined, "-n dev") {
		t.Fatalf("List must stay in the namespace: %v", fake.seen[0])
	}
}

func TestListAllNamespacesDropsTheNamespaceFlag(t *testing.T) {
	fake := &fakeRunner{out: map[string][]byte{"get pods": []byte(`{"items": []}`)}}
	c := testClient(t, fake)
	c.allNamespaces = true
	if _, err := c.List(context.Background()); err != nil {
		t.Fatalf("List: %v", err)
	}
	joined := strings.Join(fake.seen[0], " ")
	if strings.Contains(joined, "-n dev") || !strings.Contains(joined, "--all-namespaces") {
		t.Fatalf("args = %v", fake.seen[0])
	}
}

func TestStatusOnAMissingInstanceIsNotFound(t *testing.T) {
	fake := &fakeRunner{out: map[string][]byte{"get pods": []byte(`{"items": []}`)}}
	c := testClient(t, fake)
	_, err := c.Status(context.Background(), "ghost")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownWithoutAPodIsIdempotent(t *testing.T) {
	fake := &fakeRunner{out: map[string][]byte{"get pods": []byte(`{"items": []}`)}}
	c := testClient(t, fake)
	res, err := c.Down(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	if res.Removed || res.State != "absent" || res.Backend != "kube" {
		t.Fatalf("result = %+v", res)
	}
}

func TestMatchesConfigRejectsADifferentDatabase(t *testing.T) {
	c := testClient(t, &fakeRunner{})
	p := parsePod(t, runningPodJSON)
	opt := testUpOptions()
	opt.User, opt.Password, opt.Database, opt.Image = "test", "pw", "app_test", "postgres:16-alpine"
	if !c.matchesConfig(p, opt) {
		t.Fatal("an identical configuration must be reused")
	}
	opt.Database = "other_test"
	if c.matchesConfig(p, opt) {
		t.Fatal("a different database must force a replacement")
	}
}

func TestReuseTimeoutIsBounded(t *testing.T) {
	if got := reuseTimeout(3 * time.Minute); got != 30*time.Second {
		t.Fatalf("reuseTimeout(3m) = %s, want 30s: a stale pod must be replaced quickly", got)
	}
	if got := reuseTimeout(5 * time.Second); got != 5*time.Second {
		t.Fatalf("reuseTimeout(5s) = %s, want the caller's shorter budget", got)
	}
	if got := reuseTimeout(0); got != 30*time.Second {
		t.Fatalf("reuseTimeout(0) = %s", got)
	}
}

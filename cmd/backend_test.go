package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/tarcisiomiranda/devrig/internal/kube"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

func TestResolveKind(t *testing.T) {
	for _, tc := range []struct {
		flag, env string
		want      rig.Kind
	}{
		{"", "", rig.KindAuto},
		{"docker", "", rig.KindDocker},
		{"kube", "", rig.KindKube},
		{"k8s", "", rig.KindKube},
		{"kubernetes", "", rig.KindKube},
		{"", "kube", rig.KindKube},
		{"docker", "kube", rig.KindDocker},
		{" DOCKER ", "", rig.KindDocker},
	} {
		got, err := resolveKind(tc.flag, tc.env)
		if err != nil {
			t.Fatalf("resolveKind(%q, %q): %v", tc.flag, tc.env, err)
		}
		if got != tc.want {
			t.Fatalf("resolveKind(%q, %q) = %q, want %q", tc.flag, tc.env, got, tc.want)
		}
	}
}

func TestResolveKindRejectsGarbage(t *testing.T) {
	if _, err := resolveKind("podman", ""); err == nil {
		t.Fatal("expected an error for an unknown backend")
	}
}

type stubBackend struct {
	kind    rig.Kind
	status  *rig.Status
	down    *rig.DownResult
	list    []rig.Status
	err     error
	upCalls int
}

func (s *stubBackend) Kind() rig.Kind { return s.kind }
func (s *stubBackend) Close() error   { return nil }

func (s *stubBackend) Up(context.Context, rig.UpOptions) (*rig.Status, error) {
	s.upCalls++
	return s.status, s.err
}

func (s *stubBackend) Status(_ context.Context, _ string) (*rig.Status, error) {
	return s.status, s.err
}

func (s *stubBackend) Down(_ context.Context, _ string) (*rig.DownResult, error) {
	return s.down, s.err
}

func (s *stubBackend) List(context.Context) ([]rig.Status, error) { return s.list, nil }

func (s *stubBackend) URL(_ context.Context, _ string) (string, error) {
	if s.status == nil {
		return "", s.err
	}
	return s.status.URL, s.err
}

func (s *stubBackend) Logs(_ context.Context, _ string, _ int) (*rig.LogsResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &rig.LogsResult{Backend: s.kind.String()}, nil
}

func auto(primary, secondary rig.Backend) *autoBackend {
	a := newAutoBackend(primary, kube.Options{})
	a.fallback = func(context.Context) (rig.Backend, error) {
		if secondary == nil {
			return nil, errors.New("no cluster")
		}
		return secondary, nil
	}
	return a
}

// `devrig up --backend kube` followed by a bare `devrig url` must still resolve,
// even though Docker answers first and knows nothing about that instance.
func TestAutoFindsAnInstanceOnTheSecondBackend(t *testing.T) {
	missing := &stubBackend{kind: rig.KindDocker, err: &rig.NotFoundError{Name: "app", Where: "docker"}}
	found := &stubBackend{kind: rig.KindKube, status: &rig.Status{
		Name: "app", Backend: "kube", URL: "postgres://test:test@127.0.0.1:5433/app_test?sslmode=disable",
	}}

	st, err := auto(missing, found).Status(context.Background(), "app")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Backend != "kube" {
		t.Fatalf("status came from %q", st.Backend)
	}

	url, err := auto(missing, found).URL(context.Background(), "app")
	if err != nil || url == "" {
		t.Fatalf("URL = %q, err = %v", url, err)
	}
}

func TestAutoKeepsRealErrorsFromThePrimary(t *testing.T) {
	broken := &stubBackend{kind: rig.KindDocker, err: errors.New("permission denied on /var/run/docker.sock")}
	fallback := &stubBackend{kind: rig.KindKube, status: &rig.Status{Name: "app"}}

	_, err := auto(broken, fallback).Status(context.Background(), "app")
	if err == nil || !errors.Is(err, broken.err) {
		t.Fatalf("a non-not-found error must not fall through: %v", err)
	}
}

// down is idempotent, so the Docker backend reports "absent" instead of an
// error; the Kubernetes pod would survive if that ended the search.
func TestAutoDownReachesTheSecondBackend(t *testing.T) {
	absent := &stubBackend{kind: rig.KindDocker, down: &rig.DownResult{Name: "app", Backend: "docker", State: "absent"}}
	removes := &stubBackend{kind: rig.KindKube, down: &rig.DownResult{Name: "app", Backend: "kube", Removed: true, State: "absent"}}

	res, err := auto(absent, removes).Down(context.Background(), "app")
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	if !res.Removed || res.Backend != "kube" {
		t.Fatalf("result = %+v", res)
	}
}

func TestAutoListMergesBothBackends(t *testing.T) {
	first := &stubBackend{kind: rig.KindDocker, list: []rig.Status{{Name: "local", Backend: "docker"}}}
	second := &stubBackend{kind: rig.KindKube, list: []rig.Status{{Name: "remote", Backend: "kube"}}}

	items, err := auto(first, second).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || items[0].Backend != "docker" || items[1].Backend != "kube" {
		t.Fatalf("items = %+v", items)
	}
}

func TestAutoWithoutAClusterStaysOnTheFirstBackend(t *testing.T) {
	only := &stubBackend{kind: rig.KindDocker, list: []rig.Status{{Name: "local", Backend: "docker"}}}
	items, err := auto(only, nil).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
}

func TestAutoCreatesOnThePrimaryOnly(t *testing.T) {
	primary := &stubBackend{kind: rig.KindDocker, status: &rig.Status{Name: "app", Backend: "docker"}}
	secondary := &stubBackend{kind: rig.KindKube}

	if _, err := auto(primary, secondary).Up(context.Background(), rig.UpOptions{Name: "app"}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if primary.upCalls != 1 || secondary.upCalls != 0 {
		t.Fatalf("up calls: primary=%d secondary=%d", primary.upCalls, secondary.upCalls)
	}
}

func TestKubeFlagsImplyTheKubeBackend(t *testing.T) {
	t.Cleanup(func() { flagNamespace = "" })
	flagNamespace = "dev"
	if !kubeFlagsGiven() {
		t.Fatal("--namespace must imply the kube backend")
	}
}

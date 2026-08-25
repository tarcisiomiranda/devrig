package docker

import (
	"errors"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/tarcisiomiranda/devrig/internal/engine"
)

func inspectPublishing(ports nat.PortMap) container.InspectResponse {
	return container.InspectResponse{
		NetworkSettings: &container.NetworkSettings{
			NetworkSettingsBase: container.NetworkSettingsBase{Ports: ports},
		},
	}
}

func TestPublishedPortFollowsTheContainerPort(t *testing.T) {
	for _, tc := range []struct {
		engine        string
		containerPort int
		hostPort      string
		want          int
	}{
		{"postgres", 5432, "54321", 54321},
		{"mysql", 3306, "33061", 33061},
		{"custom", 6432, "16432", 16432},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			insp := inspectPublishing(nat.PortMap{
				portKey(tc.containerPort): []nat.PortBinding{
					{HostIP: "127.0.0.1", HostPort: tc.hostPort},
				},
			})
			got, err := publishedPort(insp, tc.containerPort)
			if err != nil {
				t.Fatalf("publishedPort(%d): %v", tc.containerPort, err)
			}
			if got != tc.want {
				t.Fatalf("publishedPort(%d) = %d, want %d", tc.containerPort, got, tc.want)
			}
		})
	}
}

func TestPublishedPortDoesNotAssumePostgres(t *testing.T) {
	insp := inspectPublishing(nat.PortMap{
		portKey(3306): []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "33061"}},
	})
	if _, err := publishedPort(insp, 5432); err == nil {
		t.Fatal("a container publishing 3306 must not satisfy a lookup for 5432")
	}
}

func TestPublishedPortErrorNamesThePort(t *testing.T) {
	insp := inspectPublishing(nat.PortMap{})
	_, err := publishedPort(insp, 3306)
	if err == nil {
		t.Fatal("expected an error when nothing is published")
	}
	if !strings.Contains(err.Error(), "3306/tcp") {
		t.Fatalf("error = %q, want it to name 3306/tcp", err)
	}
}

func TestPublishedPortWithoutNetworkSettings(t *testing.T) {
	if _, err := publishedPort(container.InspectResponse{}, 5432); err == nil {
		t.Fatal("expected an error when the container has no network settings")
	}
}

func TestPublishedPortPrefersLoopbackBinding(t *testing.T) {
	insp := inspectPublishing(nat.PortMap{
		portKey(5432): []nat.PortBinding{
			{HostIP: "192.168.1.10", HostPort: "9999"},
			{HostIP: "127.0.0.1", HostPort: "54321"},
		},
	})
	got, err := publishedPort(insp, 5432)
	if err != nil {
		t.Fatalf("publishedPort: %v", err)
	}
	if got != 54321 {
		t.Fatalf("publishedPort = %d, want the loopback binding 54321", got)
	}
}

func TestContainerPortHintOnlyFiresOnOverride(t *testing.T) {
	spec := engine.Default()
	base := errors.New("postgres not query-ready")

	plain := containerPortHint(base, UpOptions{Engine: spec, ContainerPort: spec.ContainerPort})
	if strings.Contains(plain.Error(), "hint:") {
		t.Fatalf("no hint expected without an override: %v", plain)
	}

	over := containerPortHint(base, UpOptions{
		Engine:        spec,
		ContainerPort: 6432,
		Image:         "postgres:16-alpine",
	})
	if !strings.Contains(over.Error(), "--container-port 6432") {
		t.Fatalf("hint should name the override: %v", over)
	}
	if !errors.Is(over, base) {
		t.Fatalf("hint must wrap the original error: %v", over)
	}
}

func TestResolveContainerPortFallsBackToTheEngine(t *testing.T) {
	if got := resolveContainerPort(0, 5432); got != 5432 {
		t.Fatalf("resolveContainerPort(0, 5432) = %d, want 5432", got)
	}
	if got := resolveContainerPort(6432, 5432); got != 6432 {
		t.Fatalf("resolveContainerPort(6432, 5432) = %d, want the override 6432", got)
	}
}

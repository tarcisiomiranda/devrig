// Package rig holds the types every devrig backend speaks and the interface
// they implement. It exists so the CLI depends on the contract, not on Docker.
package rig

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tarcisiomiranda/devrig/internal/engine"
)

// Kind names a backend implementation.
type Kind string

const (
	KindDocker Kind = "docker"
	KindKube   Kind = "kube"
	KindAuto   Kind = "auto"
)

func (k Kind) String() string { return string(k) }

// Status is the public JSON shape for an instance.
type Status struct {
	Name          string `json:"name"`
	Backend       string `json:"backend"`
	Engine        string `json:"engine"`
	Namespace     string `json:"namespace,omitempty"`
	Context       string `json:"context,omitempty"`
	ContainerID   string `json:"container_id,omitempty"`
	ContainerName string `json:"container_name"`
	State         string `json:"state"`
	Ready         bool   `json:"ready"`
	Host          string `json:"host"`
	Port          int    `json:"port,omitempty"`
	ContainerPort int    `json:"container_port,omitempty"`
	User          string `json:"user"`
	Password      string `json:"password"`
	Database      string `json:"database"`
	Image         string `json:"image"`
	URL           string `json:"url,omitempty"`
}

// DownResult is the JSON shape for down.
type DownResult struct {
	Name    string `json:"name"`
	Backend string `json:"backend"`
	Removed bool   `json:"removed"`
	State   string `json:"state"`
}

// LogsResult is the JSON shape for logs.
type LogsResult struct {
	Name    string `json:"name"`
	Backend string `json:"backend"`
	Logs    string `json:"logs"`
}

// UpOptions configures a new instance.
type UpOptions struct {
	Name          string
	Engine        *engine.Spec
	User          string
	Password      string
	Database      string
	Image         string
	Port          int // 0 = ephemeral host port
	ContainerPort int // 0 = the port the engine spec declares
	Timeout       time.Duration
}

// Backend runs instances somewhere: a Docker daemon, a Kubernetes cluster.
type Backend interface {
	Kind() Kind
	Up(ctx context.Context, opt UpOptions) (*Status, error)
	Down(ctx context.Context, name string) (*DownResult, error)
	Status(ctx context.Context, name string) (*Status, error)
	List(ctx context.Context) ([]Status, error)
	URL(ctx context.Context, name string) (string, error)
	Logs(ctx context.Context, name string, tail int) (*LogsResult, error)
	Close() error
}

// NotFoundError says a backend has no instance under that name.
type NotFoundError struct {
	Name  string
	Where string
}

func (e *NotFoundError) Error() string {
	if e.Where == "" {
		return fmt.Sprintf("instance %q not found", e.Name)
	}
	return fmt.Sprintf("instance %q not found in %s", e.Name, e.Where)
}

// IsNotFound reports whether err means "no such instance".
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

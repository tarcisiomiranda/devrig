package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/tarcisiomiranda/devrig/internal/docker"
	"github.com/tarcisiomiranda/devrig/internal/kube"
	"github.com/tarcisiomiranda/devrig/internal/out"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

var (
	flagBackend       string
	flagNamespace     string
	flagContext       string
	flagKubeconfig    string
	flagAllNamespaces bool
	flagCPU           string
	flagMemory        string
)

// resolveKind picks a backend from the flag, then the environment, then auto.
func resolveKind(flag, env string) (rig.Kind, error) {
	value := strings.ToLower(strings.TrimSpace(flag))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(env))
	}
	switch value {
	case "":
		return rig.KindAuto, nil
	case "docker":
		return rig.KindDocker, nil
	case "kube", "kubectl", "kubernetes", "k8s":
		return rig.KindKube, nil
	case "auto":
		return rig.KindAuto, nil
	default:
		return "", fmt.Errorf("unknown backend %q — use docker, kube or auto", value)
	}
}

// kubeFlagsGiven reports whether the invocation only makes sense on Kubernetes.
func kubeFlagsGiven() bool {
	return flagNamespace != "" || flagContext != "" || flagKubeconfig != "" || flagAllNamespaces ||
		flagCPU != "" || flagMemory != ""
}

func kubeOptions() kube.Options {
	namespace := flagNamespace
	if namespace == "" {
		namespace = os.Getenv("DEVRIG_NAMESPACE")
	}
	contextName := flagContext
	if contextName == "" {
		contextName = os.Getenv("DEVRIG_CONTEXT")
	}
	kubeconfig := flagKubeconfig
	if kubeconfig == "" {
		kubeconfig = os.Getenv("DEVRIG_KUBECONFIG")
	}
	return kube.Options{
		Binary:        os.Getenv("DEVRIG_KUBECTL"),
		Context:       contextName,
		Namespace:     namespace,
		Kubeconfig:    kubeconfig,
		AllNamespaces: flagAllNamespaces,
		CPU:           flagCPU,
		Memory:        flagMemory,
	}
}

// selectBackend opens the backend the flags, the environment or the machine imply.
func selectBackend(ctx context.Context) (rig.Backend, error) {
	kind, err := resolveKind(flagBackend, os.Getenv("DEVRIG_BACKEND"))
	if err != nil {
		return nil, err
	}
	if kind == rig.KindAuto && kubeFlagsGiven() {
		kind = rig.KindKube
	}
	switch kind {
	case rig.KindDocker:
		return docker.New(ctx)
	case rig.KindKube:
		return kube.New(ctx, kubeOptions())
	default:
		dockerClient, dockerErr := docker.New(ctx)
		if dockerErr == nil {
			return newAutoBackend(dockerClient, kubeOptions()), nil
		}
		kubeClient, kubeErr := kube.New(ctx, kubeOptions())
		if kubeErr == nil {
			return kubeClient, nil
		}
		return nil, backendUnavailable(dockerErr, kubeErr)
	}
}

func firstLine(err error) string {
	if err == nil {
		return ""
	}
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}

func withBackend(fn func(ctx context.Context, b rig.Backend) error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b, err := selectBackend(ctx)
	if err != nil {
		out.Fatal(err)
	}
	defer b.Close()

	if err := fn(ctx, b); err != nil {
		out.Fatal(err)
	}
}

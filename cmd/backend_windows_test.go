//go:build windows

package cmd

import (
	"context"
	"strings"
	"testing"
)

func TestWindowsRejectsKubeBeforeConnecting(t *testing.T) {
	previous := flagBackend
	defer func() { flagBackend = previous }()
	flagBackend = "kube"
	t.Setenv("DEVRIG_KUBECTL", "this-command-must-never-run")
	_, err := selectBackend(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not supported on Windows") {
		t.Fatalf("expected Windows backend error, got %v", err)
	}
}

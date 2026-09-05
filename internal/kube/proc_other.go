//go:build !unix

package kube

import (
	"errors"
	"os/exec"
)

func setDetached(cmd *exec.Cmd) {}

func processAlive(pid int) bool { return false }

func killGroup(pid int) error {
	return errors.New("the kube backend needs a unix host (Linux or macOS)")
}

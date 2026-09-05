//go:build unix

package kube

import (
	"os/exec"
	"syscall"
)

// setDetached puts kubectl in its own session so the forward survives devrig.
func setDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// killGroup signals the whole session, so kubectl's children go with it.
func killGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		return syscall.Kill(pid, syscall.SIGTERM)
	}
	return nil
}

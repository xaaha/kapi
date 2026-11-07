//go:build !windows

// Package crashtest kills the current process the way a power cut would, for tests that check recovery.
package crashtest

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func KillSelf() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL) }

func Killed(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.Sys().(syscall.WaitStatus).Signal() == syscall.SIGKILL
}

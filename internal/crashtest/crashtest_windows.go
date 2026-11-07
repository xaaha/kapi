// Package crashtest kills the current process the way a power cut would, for tests that check recovery.
package crashtest

import (
	"errors"
	"os/exec"

	"golang.org/x/sys/windows"
)

const killedCode = 137

func KillSelf() { _ = windows.TerminateProcess(windows.CurrentProcess(), killedCode) }

func Killed(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == killedCode
}

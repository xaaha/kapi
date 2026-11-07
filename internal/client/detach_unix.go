//go:build !windows

package client

import (
	"os"
	"strconv"
	"syscall"
)

func detachedProcess() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func closeInheritedOnExec() error {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if fd, err := strconv.Atoi(e.Name()); err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
	return nil
}

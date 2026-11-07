package client

import (
	"syscall"

	"golang.org/x/sys/windows"
)

func detachedProcess() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
}

func closeInheritedOnExec() error { return nil }

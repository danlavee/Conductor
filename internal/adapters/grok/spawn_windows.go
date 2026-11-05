//go:build windows

package grok

import "os/exec"
import "syscall"

func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008,
		HideWindow:    true,
	}
}

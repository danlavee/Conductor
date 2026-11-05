//go:build !windows

package grok

import "os/exec"
import "syscall"

func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

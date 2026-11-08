//go:build windows

package hold

import (
	"os/exec"
	"syscall"
)

// detach keeps the holder out of the hook's console and process group, so the
// host neither awaits it nor kills it with the hook.
func detach(command *exec.Cmd) {
	const detachedProcess = 0x00000008
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
		HideWindow:    true,
	}
}

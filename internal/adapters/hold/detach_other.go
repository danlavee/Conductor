//go:build !windows

package hold

import (
	"os/exec"
	"syscall"
)

// detach puts the holder in its own session, so the host neither awaits it
// nor kills it with the hook's process group.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

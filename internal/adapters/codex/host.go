package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

// HookTarget is the native hook's lifetime, not the shared Codex executable's.
// Windows retains the PowerShell wrapper; Unix exec replaces that wrapper.
func HookTarget() (platform.Process, error) {
	pid := os.Getpid()
	if runtime.GOOS == "windows" {
		pid = os.Getppid()
	}
	return platform.ProcessOf(pid)
}

// ResolveQueueHost finds the nearest native Codex executable, never PATH.
// The executable activates turns; it does not determine the hook's lifetime.
func ResolveQueueHost() (func(context.Context, string, string) error, error) {
	for pid, depth := os.Getppid(), 0; pid > 0 && depth < 64; depth++ {
		process, err := platform.InspectProcess(pid)
		if err != nil {
			return nil, err
		}
		base := filepath.Base(process.Executable)
		if strings.EqualFold(base, "codex") || strings.EqualFold(base, "codex.exe") {
			return func(ctx context.Context, session, text string) error {
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				output, err := exec.CommandContext(ctx, process.Executable, "queue", "--thread", session, "--message", text).CombinedOutput()
				if err != nil {
					return fmt.Errorf("Codex rejected the queued turn: %w: %s", err, strings.TrimSpace(string(output)))
				}
				return nil
			}, nil
		}
		if process.ParentPID == pid {
			break
		}
		pid = process.ParentPID
	}
	return nil, errors.New("native Codex executable is absent from hook ancestry")
}

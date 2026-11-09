package main

import (
	"io"
	"os"
	"runtime"
	"strings"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/agy"
	"github.com/danlavee/Conductor/internal/adapters/hold"
	"github.com/danlavee/Conductor/internal/install"
	"github.com/danlavee/Conductor/internal/platform"
	conductor "github.com/danlavee/Conductor/internal/state"
)

var agyHost = adapterHost{
	binding:     agy.AdapterName,
	environment: agy.SessionEnvironment,
	payload: install.Payload{
		Bundle: adapterbundle.AGY, Parent: "adapters", Name: agy.AdapterName, ExecutableDir: "bin",
		HookCommand: agyHookCommand,
	},
	verbs:  []string{"identity", "arm"},
	input:  "(the hook payload on stdin)",
	event:  agyEvent,
	target: agyTarget,
	// The host reads a Stop hook's continue decision on a clean exit.
	wake: func(batch conductor.Batch) ([]conductor.ReadResult, error) {
		reason, shown := conductor.RenderWake(batch)
		return shown, conductor.WriteJSON(os.Stdout, stopDelivery{Decision: agy.StopDecision, Reason: reason})
	},
	// The host's verified no-op is an empty object.
	idle: func() error { return conductor.WriteJSON(os.Stdout, struct{}{}) },
}

// agyEvent takes the session from the payload, and arms only at the end of a
// turn the model ended itself.
func agyEvent(verb string, args []string, stdin io.Reader) (string, bool, error) {
	if len(args) != 0 {
		return "", false, errUsage
	}
	hook, err := agy.ReadHook(stdin)
	return hook.ConversationID, verb != "arm" || hook.EndsTurn(), err
}

// agyTarget is the session process: the host wraps each hook command in a shell.
func agyTarget() (platform.Process, error) { return hold.AncestorTarget(os.Getppid(), agy.Shell) }

// agyHookCommand names the installed binary relative to the plugin root, the
// working directory the host runs hooks in; on Windows with backslashes,
// escaped for the JSON they land in.
func agyHookCommand(executable string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(executable, "/", `\\`)
	}
	return "./" + executable
}

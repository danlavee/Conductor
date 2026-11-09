package agy

import (
	"strings"
	"testing"

	"github.com/danlavee/Conductor/internal/platform"
)

// Only a turn the model ended itself is one a delivery may continue; a cancel,
// an error or a cap is the host or the user stopping the loop.
func TestOnlyAModelEndedTurnIsContinued(t *testing.T) {
	for reason, want := range map[string]bool{
		"NO_TOOL_CALL": true, "": false, "USER_CANCELED": false, "ERROR": false,
		"MAX_INVOCATIONS": false, "MAX_FORCED_INVOCATIONS": false, "TERMINAL_CUSTOM_HOOK": false,
	} {
		if got := (Hook{TerminationReason: reason}).EndsTurn(); got != want {
			t.Errorf("EndsTurn(%q) = %v, want %v", reason, got, want)
		}
	}
}

// The payload is the hook's only session source: the environment a model's
// command shares with hooks must not stand in for it.
func TestReadHookTakesTheSessionFromThePayloadAlone(t *testing.T) {
	t.Setenv(SessionEnvironment, "from-env")
	if hook, err := ReadHook(strings.NewReader(`{"conversationId":"conv-1"}`)); err != nil || hook.ConversationID != "conv-1" {
		t.Fatalf("ReadHook = %+v, %v", hook, err)
	}
	for _, payload := range []string{"", "{}", "not json"} {
		if _, err := ReadHook(strings.NewReader(payload)); err == nil {
			t.Errorf("ReadHook(%q) succeeded", payload)
		}
	}
}

func TestShellSkipsOnlyTheHookWrapper(t *testing.T) {
	for executable, want := range map[string]bool{
		"CMD.EXE": true, "/bin/sh": true, "/usr/bin/bash": true, "/bin/dash": true,
		"Antigravity.exe": false, "/usr/bin/agy": false,
	} {
		if got := Shell(platform.ProcessInstance{Executable: executable}); got != want {
			t.Errorf("Shell(%s) = %v, want %v", executable, got, want)
		}
	}
}

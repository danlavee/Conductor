package main

import (
	"path/filepath"
	"testing"

	"github.com/danlavee/Conductor/internal/adapters/agy"
)

// The payload is a hook's only session source, so a hook run without one fails
// even inside a conversation the environment names.
func TestAGYHooksFailWithoutAPayload(t *testing.T) {
	inAGY := cli{home: filepath.Join(t.TempDir(), "runtime-state"), env: map[string]string{agy.SessionEnvironment: "conv-1"}}
	for _, args := range [][]string{{"adapter", "agy", "identity"}, {"adapter", "agy", "arm"}, {"adapter", "agy", "arm", "--hook"}} {
		if _, _, err := inAGY.run(args...); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

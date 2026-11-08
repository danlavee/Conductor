package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitForOwner waits for a holder's owner record, which appears once it holds
// the guard; probing the guard before then would take it first.
func waitForOwner(t *testing.T, root, agent string) string {
	t.Helper()
	owner := filepath.Join(root, "state", "watch", agent+".owner.json")
	waitFor(t, "a holder to take the guard", func() bool {
		_, err := os.Stat(owner)
		return err == nil
	})
	return owner
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !condition(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
	}
}

package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestHelpIsAskedForTwoWaysAndIsAnOperandEverywhereElse pins both halves. An
// agent meeting an unfamiliar binary asks it what it does, and answering that
// with a usage error taught the reader the binary was broken. The other half
// costs more if it goes wrong: `put` takes arbitrary text, so treating a help
// token in operand position as a request for usage would silently swallow a
// publication instead of making it.
func TestHelpIsAskedForTwoWaysAndIsAnOperandEverywhereElse(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"},
		{"planner", "--help"}, {"planner", "-h"}, {"planner", "help"},
	} {
		if !helpRequested(args) {
			t.Errorf("%v was not read as a request for the surface", args)
		}
	}
	for _, args := range [][]string{
		{"planner", "put", "dev/tasks", "--help"},
		{"planner", "put", "dev/tasks", "help"},
		{"planner", "put", "help", "-h"},
		{"install", "--help", "--replace"},
		{"planner", "join", "help"},
	} {
		if helpRequested(args) {
			t.Errorf("%v was read as a request for the surface rather than as operands", args)
		}
	}
}

// The printed surface and the surface a malformed command reports must be the
// same text: an agent that reaches one and then the other, and finds them
// different, has to work out which one the binary actually has.
func TestPrintedHelpAndUsageErrorAreOneStatement(t *testing.T) {
	if usageError().Error() != usageText {
		t.Fatal("the usage error and the printed surface have diverged")
	}
}

// TestAskingTheBinaryWhatItDoesAnswersOnTheJSONWire runs the ask end to end,
// because what failed was an exit code and a stream and neither is reachable
// from the predicate. stdout carries JSON for every other command, so the one
// command an unfamiliar agent reaches for first must not be the exception that
// teaches its reader otherwise.
func TestAskingTheBinaryWhatItDoesAnswersOnTheJSONWire(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	for _, args := range [][]string{{"--help"}, {"planner", "--help"}} {
		stdout, stderr, err := cli{home: state, dir: t.TempDir()}.run(args...)
		if err != nil {
			t.Fatalf("%v: %v: %s", args, err, stderr)
		}
		var answer usageResult
		if err := json.Unmarshal(stdout, &answer); err != nil {
			t.Fatalf("%v wrote %s, which a reader of this binary's stdout cannot decode", args, stdout)
		}
		if answer.Usage != usageText {
			t.Fatalf("%v answered a surface this binary does not have", args)
		}
	}
}

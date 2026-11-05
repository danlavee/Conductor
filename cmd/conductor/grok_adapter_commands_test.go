package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danlavee/Conductor/internal/adapters/grok"
)

func TestGrokInstallRewritesTheHookExecutable(t *testing.T) {
	destination := filepath.Join(t.TempDir(), ".conductor", "adapters", "grok")
	stdout, stderr, err := runCLIHelper(os.Args[0], t.TempDir(), "", "adapter", "grok", "install", destination)
	if err != nil {
		t.Fatalf("adapter install: %v: %s", err, stderr)
	}
	var result struct {
		Status     string `json:"status"`
		BinaryPath string `json:"binary_path"`
	}
	if err := json.Unmarshal(stdout, &result); err != nil {
		t.Fatalf("install result = %s: %v", stdout, err)
	}
	if result.Status != "installed" {
		t.Fatalf("install result = %+v", result)
	}
	expected := filepath.Join(destination, "bin", "conductor")
	if runtime.GOOS == "windows" {
		expected += ".exe"
	}
	if result.BinaryPath != expected {
		t.Fatalf("binary at %s, hooks look in %s", result.BinaryPath, expected)
	}
	data, err := os.ReadFile(filepath.Join(destination, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := "${GROK_PLUGIN_ROOT}/bin/conductor"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if !strings.Contains(string(data), want) || strings.Contains(string(data), grok.HookCommandPlaceholder) {
		t.Fatalf("installed hooks = %s", data)
	}
	if _, err := os.Stat(filepath.Join(destination, ".grok-plugin", "marketplace.json")); err != nil {
		t.Fatal(err)
	}
}

func TestGrokIdentityReportsWhenItDoesNotArm(t *testing.T) {
	stdout, stderr, err := runAdapterHelper(t, t.TempDir(), t.TempDir(), "session-1", "adapter", "grok", "identity")
	if err != nil {
		t.Fatalf("identity: %v: %s", err, stderr)
	}
	var report map[string]string
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("identity report = %s: %v", stdout, err)
	}
	if !strings.Contains(report["systemMessage"], "did not arm") {
		t.Fatalf("identity report = %s", stdout)
	}
}

func TestGrokStopIgnoresSessionEnd(t *testing.T) {
	command := exec.Command(os.Args[0], "adapter", "grok", "stop", "--session=session-1")
	command.Dir = t.TempDir()
	command.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	command.Env = replaceEnvironment(command.Env, "CONDUCTOR_HOME", t.TempDir())
	command.Env = replaceEnvironment(command.Env, grok.SessionEnvironment, "")
	command.Stdin = strings.NewReader(`{"reason":"shutdown","lastAssistantMessage":"token"}`)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("session-end stop: %v: %s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("session-end stop wrote %s", stdout.String())
	}
}

package state

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const protocolHelperEnvironment = "CONDUCTOR_PROTOCOL_NEW_HELPER"

func TestNewDeclaresProtocolBeforeStateDirectories(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	if _, err := New(home, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(protocolPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != fmt.Sprintf("{\n  \"version\": %d\n}\n", CurrentProtocolVersion) {
		t.Fatalf("protocol declaration = %q", data)
	}
	for _, name := range []string{"registry", "topics", "locks", "cursors", "state"} {
		if info, err := os.Stat(filepath.Join(home, name)); err != nil || !info.IsDir() {
			t.Fatalf("state directory %s is unavailable: %v", name, err)
		}
	}
}

func TestNewRejectsVersionOneWithoutChangingIt(t *testing.T) {
	home := t.TempDir()
	want := []byte("{\n  \"version\": 1\n}\n")
	if err := os.WriteFile(protocolPath(home), want, 0o600); err != nil {
		t.Fatal(err)
	}
	found := 1
	_, err := New(home, "")
	assertProtocolMismatch(t, err, &found)
	got, readErr := os.ReadFile(protocolPath(home))
	if readErr != nil || !bytes.Equal(got, want) {
		t.Fatalf("version-one declaration changed: %q, %v", got, readErr)
	}
}

func TestNewRejectsInvalidProtocolDeclarations(t *testing.T) {
	negative, zero, unsupported := -1, 0, CurrentProtocolVersion+1
	for name, test := range map[string]struct {
		data  string
		found *int
	}{
		"unsupported": {fmt.Sprintf(`{"version":%d}`, unsupported), &unsupported},
		"zero":        {`{"version":0}`, &zero},
		"negative":    {`{"version":-1}`, &negative},
		"missing":     {`{}`, nil},
		"unknown":     {`{"version":1,"extra":true}`, nil},
		"trailing":    {`{"version":1}{}`, nil},
		"malformed":   {`{"version":`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(protocolPath(home), []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := New(home, "")
			assertProtocolMismatch(t, err, test.found)
		})
	}
}

func TestConcurrentNewPublishesOneProtocol(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	const clients = 8
	type process struct {
		command *exec.Cmd
		output  bytes.Buffer
	}
	processes := make([]process, clients)
	for index := range processes {
		command := exec.Command(os.Args[0], "-test.run=^TestProtocolNewHelperProcess$")
		command.Env = append(os.Environ(), protocolHelperEnvironment+"="+home)
		command.Stdout, command.Stderr = &processes[index].output, &processes[index].output
		processes[index].command = command
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, process := range processes {
		if err := process.command.Wait(); err != nil {
			t.Fatalf("initializer failed: %v\n%s", err, process.output.String())
		}
	}
	if err := checkProtocol(home); err != nil {
		t.Fatalf("protocol validation = %v", err)
	}
	if temps, _ := filepath.Glob(filepath.Join(home, ".protocol-*.tmp")); len(temps) != 0 {
		t.Fatalf("initializers left temporaries: %v", temps)
	}
}

func TestProtocolNewHelperProcess(t *testing.T) {
	home := os.Getenv(protocolHelperEnvironment)
	if home == "" {
		return
	}
	if _, err := New(home, ""); err != nil {
		t.Fatal(err)
	}
}

func TestStatefulBoundariesDoNotRecreateMissingProtocol(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "agent")
	if _, err := client.Join("agent", "coordination"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(protocolPath(home)); err != nil {
		t.Fatal(err)
	}
	checks := map[string]func() error{
		"resolve":      func() error { _, err := client.ResolveAgent(); return err },
		"join":         func() error { _, err := client.Join("other", "review"); return err },
		"leave":        func() error { return client.Leave("agent") },
		"list":         func() error { _, err := client.ListAgents(); return err },
		"snapshot":     func() error { _, err := client.FullSnapshot(); return err },
		"ack-snapshot": func() error { return client.AcknowledgeSnapshot(Snapshot{}) },
		"get":          func() error { _, err := client.Get(ReadRequest{Topic: "messages/team", Mode: ReadFull}); return err },
		"ack-read":     func() error { return client.AcknowledgeRead(ReadResult{}) },
		"watch":        func() error { _, err := client.Watch(); return err },
		"watch-owner":  func() error { _, err := client.AcquireWatchOwnership(); return err },
		"put":          func() error { _, err := client.Put("messages/team", "text"); return err },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) { assertProtocolMismatch(t, check(), nil) })
	}
	if _, statErr := os.Stat(protocolPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stateful call recreated protocol declaration: %v", statErr)
	}
}

func assertProtocolMismatch(t *testing.T, err error, found *int) {
	t.Helper()
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.Code != "PROTOCOL_MISMATCH" || protocolErr.Protocol == nil || protocolErr.Protocol.Supported != CurrentProtocolVersion {
		t.Fatalf("error = %#v, want protocol mismatch", err)
	}
	if found == nil {
		if protocolErr.Protocol.Found != nil {
			t.Fatalf("found = %d, want omitted", *protocolErr.Protocol.Found)
		}
		return
	}
	if protocolErr.Protocol.Found == nil || *protocolErr.Protocol.Found != *found {
		t.Fatalf("found = %v, want %d", protocolErr.Protocol.Found, *found)
	}
}

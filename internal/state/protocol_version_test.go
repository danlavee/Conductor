package state

import (
	"bytes"
	"context"
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
	for _, name := range []string{"registry", "topics", "locks", "inbox", "events", "cursors", "transactions", "state"} {
		if info, err := os.Stat(filepath.Join(home, name)); err != nil || !info.IsDir() {
			t.Fatalf("state directory %s is unavailable: %v", name, err)
		}
	}
}

func TestNewRejectsUnversionedStateWithoutChangingIt(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, "legacy.json")
	want := []byte("legacy bytes")
	if err := os.WriteFile(legacy, want, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(home, "")
	assertProtocolMismatch(t, err, nil)
	got, readErr := os.ReadFile(legacy)
	if readErr != nil || string(got) != string(want) {
		t.Fatalf("legacy state changed: %q, %v", got, readErr)
	}
	if _, statErr := os.Stat(protocolInitPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("initializer wrote into legacy state: %v", statErr)
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

func TestNewRecoversRecognizedInitializationSupport(t *testing.T) {
	for name, orphan := range map[string]bool{"empty": false, "orphan-temp": true} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			support := protocolInitPath(home)
			if err := os.Mkdir(support, 0o700); err != nil {
				t.Fatal(err)
			}
			if orphan {
				if err := os.WriteFile(filepath.Join(support, "protocol-orphan.tmp"), []byte("partial"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := New(home, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(protocolPath(home)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNewRejectsUnknownInitializationSupport(t *testing.T) {
	home := t.TempDir()
	support := protocolInitPath(home)
	if err := os.Mkdir(support, 0o700); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(support, "unknown")
	if err := os.WriteFile(unknown, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(home, "")
	assertProtocolMismatch(t, err, nil)
	if data, readErr := os.ReadFile(unknown); readErr != nil || string(data) != "preserve" {
		t.Fatalf("unknown support entry changed: %q, %v", data, readErr)
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
	if exists, err := validateExistingProtocol(home); err != nil || !exists {
		t.Fatalf("protocol validation = %t, %v", exists, err)
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
	if _, err := client.Register("agent", "coordination"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(protocolPath(home)); err != nil {
		t.Fatal(err)
	}
	checks := map[string]func() error{
		"resolve":      func() error { _, err := client.ResolveAgent(); return err },
		"register":     func() error { _, err := client.Register("other", "review"); return err },
		"deregister":   func() error { return client.Deregister("agent") },
		"list":         func() error { _, err := client.ListAgents(); return err },
		"snapshot":     func() error { _, err := client.FullSnapshot(); return err },
		"ack-snapshot": func() error { return client.AcknowledgeSnapshot(Snapshot{}) },
		"get":          func() error { _, err := client.Get(ReadRequest{Topic: "messages/team", Mode: ReadFull}); return err },
		"ack-read":     func() error { return client.AcknowledgeRead(ReadResult{}) },
		"watch":        func() error { _, err := client.WatchSinceContext(context.Background(), 0); return err },
		"ack-summary":  func() error { return client.AcknowledgeSummary(Summary{}) },
		"watch-owner":  func() error { _, err := client.AcquireWatchOwnership(); return err },
		"begin":        func() error { return client.Begin("messages/team") },
		"stage-put":    func() error { _, err := client.StagePut("text"); return err },
		"commit":       func() error { _, err := client.Commit(); return err },
		"put":          func() error { _, err := client.Put("messages/team", "text"); return err },
		"abort":        func() error { return client.Abort() },
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

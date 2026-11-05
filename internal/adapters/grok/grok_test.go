package grok

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/state"
)

func TestStopIgnoresSubagentAndSessionEnd(t *testing.T) {
	root := t.TempDir()
	for _, in := range []StopInput{{Reason: "end_turn", Subagent: "worker"}, {Reason: "shutdown"}} {
		reason, _, err := Stop(context.Background(), root, in)
		if err != nil || reason != "" {
			t.Fatalf("Stop(%+v) = %q, %v", in, reason, err)
		}
	}
}

func TestReceiptIsPresentedAndAcknowledgedFromTheAssistantLine(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".conductor-agent"), []byte("planner"), 0o644); err != nil {
		t.Fatal(err)
	}
	owner, err := state.New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	home := owner.Home
	if _, err := owner.Join("planner", "plans"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Join("writer", "writes"); err != nil {
		t.Fatal(err)
	}
	if err := binding.Bind(home, AdapterName, "session-1", "planner"); err != nil {
		t.Fatal(err)
	}
	if err := setFlag(home, "announced", "session-1"); err != nil {
		t.Fatal(err)
	}
	agent, err := state.Open(home, "planner")
	if err != nil {
		t.Fatal(err)
	}
	agent.PollInterval = time.Millisecond
	if _, err := agent.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	drain(t, agent)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Reside(ctx, agent, "session-1") }()
	waitUntil(t, func() bool { return ResidentAlive(home, "planner", "session-1") })
	writer, err := state.Open(home, "writer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Put("dev/tasks", "hello from the bus"); err != nil {
		t.Fatal(err)
	}
	var token string
	waitUntil(t, func() bool {
		select {
		case err := <-done:
			t.Fatalf("reside exited: %v", err)
		default:
		}
		shown, err := loadPresentation(home, "planner")
		if err != nil {
			t.Fatal(err)
		}
		if shown != nil && shown.Token != "" {
			token = shown.Token
			return true
		}
		return false
	})
	reason, _, err := Stop(context.Background(), home, StopInput{Session: "session-1", Project: project, Reason: "end_turn"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reason, token) || strings.Contains(reason, "hello from the bus") {
		t.Fatalf("reason = %q", reason)
	}
	ackCtx, ackCancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer ackCancel()
	reason, _, err = Stop(ackCtx, home, StopInput{Session: "session-1", Project: project, Reason: "end_turn", LastAssistant: "worked\n" + token})
	if err != nil || reason != "" {
		t.Fatalf("ack Stop = %q, %v", reason, err)
	}
	shown, err := loadPresentation(home, "planner")
	if err != nil || shown != nil {
		t.Fatalf("presentation after ack = %+v, %v", shown, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reside did not exit")
	}
}

func TestRewriteHookCommandNamesTheWindowsExecutable(t *testing.T) {
	got := string(RewriteHookCommand("hooks/hooks.json", []byte(HookCommandPlaceholder)))
	want := "${GROK_PLUGIN_ROOT}/bin/conductor"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Fatalf("rewrite = %q, want %q", got, want)
	}
}

func drain(t *testing.T, client *state.Client) {
	t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		batch, err := client.WatchContext(ctx)
		cancel()
		if err != nil {
			return
		}
		if err := client.AcknowledgeRead(batch.Deliveries...); err != nil {
			t.Fatal(err)
		}
	}
}

func waitUntil(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("timed out")
}

package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/adapters/hold"
	"github.com/danlavee/Conductor/internal/platform"
	"github.com/danlavee/Conductor/internal/state"
)

func TestServeQueuesTwoRenderedDeliveries(t *testing.T) {
	client, peer, session := joined(t)
	put(t, peer, "first")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan string, 2)
	done := serve(ctx, client, session, func(_ context.Context, id, text string) error {
		if id != session.ID {
			return fmt.Errorf("queued session %q, want %q", id, session.ID)
		}
		calls <- text
		return nil
	})
	first := receive(t, calls)
	if !strings.HasPrefix(first, "Conductor: 1 new record for you.\n\n[private/planner #1] from peer,") || !strings.Contains(first, "\nfirst\n") {
		t.Fatalf("first wake = %q", first)
	}
	put(t, peer, "second")
	if second := receive(t, calls); !strings.Contains(second, "\nsecond\n") || strings.Contains(second, "\nfirst\n") {
		t.Fatalf("second wake = %q", second)
	}
	cancel()
	await(t, done)
	quiet(t, client)
}

func TestServeAcknowledgesExactlyWhatTheRenderedQueueAccepted(t *testing.T) {
	client, peer, session := joined(t)
	for _, prefix := range []string{"FIRST", "SECOND", "THIRD"} {
		put(t, peer, prefix+strings.Repeat("😀", 1990))
	}
	calls := make(chan string, 2)
	failure := errors.New("queue rejected the remainder")
	count := 0
	done := serve(context.Background(), client, session, func(_ context.Context, _, text string) error {
		calls <- text
		count++
		if count == 2 {
			return failure
		}
		return nil
	})
	first, second := receive(t, calls), receive(t, calls)
	if len(utf16.Encode([]rune(first))) > state.WakeTextLimit || !strings.HasPrefix(first, "Conductor: 2 new records") || !strings.Contains(first, "1 more unread") || strings.Contains(first, "THIRD") {
		t.Fatalf("first capped wake framing = %q ... %q", first[:50], first[len(first)-60:])
	}
	if !strings.Contains(second, "THIRD") || strings.Contains(second, "SECOND") {
		t.Fatal("unshown publication did not reach the next queue")
	}
	if err := receiveError(t, done); !errors.Is(err, failure) {
		t.Fatalf("queue error = %v", err)
	}
	unread, err := client.Unread()
	if err != nil || len(unread.Deliveries) != 1 || len(unread.Deliveries[0].Publications) != 1 || !strings.HasPrefix(unread.Deliveries[0].Publications[0].Records[0].Text, "THIRD") {
		t.Fatalf("unread after rejection = %+v, %v", unread, err)
	}
}

func TestQueueFailureLeavesWorkForTheNextHookProcess(t *testing.T) {
	client, peer, session := joined(t)
	put(t, peer, "replay after rejection")
	batch, err := client.Unread()
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := state.RenderWake(batch)
	command := exec.Command(os.Args[0], "-test.run=^TestRejectedHookProcess$")
	command.Env = append(os.Environ(), "CONDUCTOR_CODEX_TEST_ROOT="+client.Home)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rejected hook process: %v: %s", err, output)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan string, 1)
	done := serve(ctx, client, session, func(_ context.Context, _, text string) error { calls <- text; return nil })
	if got := receive(t, calls); got != expected {
		t.Fatalf("replayed queue = %q, want %q", got, expected)
	}
	cancel()
	await(t, done)
	quiet(t, client)
}

// A rejected native hook exits, leaving a waker record for a dead process.
func TestRejectedHookProcess(t *testing.T) {
	root := os.Getenv("CONDUCTOR_CODEX_TEST_ROOT")
	if root == "" {
		return
	}
	client, err := state.Open(root, "planner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := platform.ProcessOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("native queue rejected")
	session := hold.Session{Binding: AdapterName, ID: "session", Target: target}
	err = hold.Hold(context.Background(), client, session, func(ctx context.Context) error {
		return Serve(ctx, client, session, func(context.Context, string, string) error { return failure })
	})
	if !errors.Is(err, failure) {
		t.Fatalf("rejected queue = %v", err)
	}
	release, err := client.AcquireWatchOwnership(state.HolderSelf, "")
	if err != nil {
		t.Fatalf("rejected queue retained the guard: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestHookLifetimeIsIndependentOfTheQueueExecutable(t *testing.T) {
	target, err := HookTarget()
	expected := os.Getpid()
	if runtime.GOOS == "windows" {
		expected = os.Getppid()
	}
	if err != nil || target.PID != expected || target.Start == "" || !target.Alive() {
		t.Fatalf("hook target = %+v, %v; want process %d", target, err, expected)
	}
}

func TestNativeHookDeathEndsTheServeHold(t *testing.T) {
	client, _, session := joined(t)
	command := exec.Command(os.Args[0], "-test.run=^TestHookLifetimeProcess$")
	command.Env = append(os.Environ(), "CONDUCTOR_CODEX_TEST_HOOK=1")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		input.Close()
		t.Fatal(err)
	}
	defer func() { input.Close(); command.Wait() }()
	session.Target, err = platform.ProcessOf(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := serve(ctx, client, session, func(context.Context, string, string) error { return errors.New("unexpected wake") })
	deadline := time.Now().Add(5 * time.Second)
	for {
		roster, err := client.Roster()
		if err != nil {
			t.Fatal(err)
		}
		holding := false
		for _, agent := range roster {
			if agent.Name == client.Agent {
				holding = agent.Wakeable
			}
		}
		if holding {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native hook never held its stream")
		}
		time.Sleep(client.PollInterval)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	await(t, done)
	release, err := client.AcquireWatchOwnership(state.HolderSelf, "")
	if err != nil {
		t.Fatalf("native hook death retained ownership: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestHookLifetimeProcess(t *testing.T) {
	if os.Getenv("CONDUCTOR_CODEX_TEST_HOOK") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
}

func TestHookEnvelopeUsesNativeSessionAndSubagentFields(t *testing.T) {
	for _, event := range []string{"SessionStart", "Stop", "PostToolUse", "SessionEnd"} {
		input := fmt.Sprintf(`{"session_id":"s1","hook_event_name":%q,"agent_id":"child","cwd":"ignored","future":true}`, event)
		got, err := ParseHookEvent(strings.NewReader(input))
		if err != nil || got.SessionID != "s1" || got.AgentID != "child" || got.HookEventName != event {
			t.Fatalf("event = %+v, %v", got, err)
		}
	}
	for _, input := range []string{`{"session_id":"s","hook_event_name":"Unknown"}`, `{"session_id":"../s","hook_event_name":"Stop"}`, `{"session_id":"s","hook_event_name":"Stop"}{}`} {
		if _, err := ParseHookEvent(strings.NewReader(input)); err == nil {
			t.Fatalf("invalid envelope accepted: %s", input)
		}
	}
}

func joined(t *testing.T) (*state.Client, *state.Client, hold.Session) {
	t.Helper()
	root := t.TempDir()
	client, err := state.New(root, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("planner", "plans"); err != nil {
		t.Fatal(err)
	}
	peer, err := state.New(root, "peer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Join("peer", "reviews"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.FullSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AcknowledgeSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := binding.Serve(root, AdapterName, "session"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := binding.Bind(root, AdapterName, "session", client.Agent); err != nil {
		t.Fatal(err)
	}
	target, err := platform.ProcessOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	client.PollInterval = 5 * time.Millisecond
	return client, peer, hold.Session{Binding: AdapterName, ID: "session", Target: target}
}

func put(t *testing.T, peer *state.Client, text string) {
	t.Helper()
	if _, err := peer.Put("private/planner", text); err != nil {
		t.Fatal(err)
	}
}

func serve(ctx context.Context, client *state.Client, session hold.Session, queue func(context.Context, string, string) error) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- hold.Hold(ctx, client, session, func(ctx context.Context) error { return Serve(ctx, client, session, queue) })
	}()
	return done
}

func receive(t *testing.T, calls <-chan string) string {
	t.Helper()
	select {
	case text := <-calls:
		return text
	case <-time.After(5 * time.Second):
		t.Fatal("native queue received nothing")
		return ""
	}
}

func receiveError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("native hook did not finish")
		return nil
	}
}

func await(t *testing.T, done <-chan error) {
	t.Helper()
	if err := receiveError(t, done); err != nil {
		t.Fatal(err)
	}
}

func quiet(t *testing.T, client *state.Client) {
	t.Helper()
	batch, err := client.Unread()
	if err != nil || len(batch.Deliveries) != 0 {
		t.Fatalf("accepted wake remained unread: %+v, %v", batch, err)
	}
}

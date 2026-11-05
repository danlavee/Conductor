package agy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/state"
)

func TestStopIgnoresNonNormalTerminationReasons(t *testing.T) {
	root := t.TempDir()
	for _, reason := range []string{
		"USER_CANCELED",
		"ERROR",
		"MAX_INVOCATIONS",
		"MAX_FORCED_INVOCATIONS",
		"MAX_TOKEN_BUDGET_EXCEEDED",
		"TERMINAL_STEP_TYPE",
		"TERMINAL_CUSTOM_HOOK",
		"INJECTED_RESPONSE",
	} {
		out, err := Stop(context.Background(), root, HookInput{
			ConversationID:    "conv-1",
			TerminationReason: reason,
		}, "")
		if err != nil || out.Decision != "" || out.Reason != "" {
			t.Fatalf("reason %q produced %+v, %v; want empty StopOutput", reason, out, err)
		}
	}
}

func TestReceiptPresentedAndAcknowledgedViaAGYTranscriptJSONL(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, IdentityFile), []byte("planner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner, err := state.New(home, "")
	if err != nil {
		t.Fatal(err)
	}
	home = owner.Home
	if _, err := owner.Join("planner", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Join("writer", "dev"); err != nil {
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
	go func() { done <- Reside(ctx, agent, "conv-1") }()
	waitUntil(t, func() bool { return ResidentAlive(home, "planner", "conv-1") })

	// Verify status reports holder == "adapter" while Reside is running.
	status, err := agent.WatchStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Wakeable || status.Holder != state.HolderAdapter {
		t.Fatalf("WatchStatus = %+v; want wakeable adapter holder", status)
	}

	if _, err := owner.Put("dev/tasks", "hello from antigravity"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		shown, err := loadPresentation(home, "planner")
		return err == nil && shown != nil && shown.Token != ""
	})
	shown, err := loadPresentation(home, "planner")
	if err != nil || shown == nil {
		t.Fatal(err)
	}

	// First Stop presents the delivery and receipt token without acknowledging yet.
	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeAGYTranscript(t, transcriptPath, []string{"Initial turn completed before delivery."})

	out, err := Stop(context.Background(), home, HookInput{
		ConversationID:    "conv-1",
		WorkspacePaths:    []string{project},
		TranscriptPath:    transcriptPath,
		TerminationReason: "NO_TOOL_CALL",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != "continue" || !strings.Contains(out.Reason, shown.Token) {
		t.Fatalf("first Stop = %+v; want decision=continue with receipt token %q", out, shown.Token)
	}
	expectedPath := presentationPath(home, "planner")
	if !strings.Contains(out.Reason, expectedPath) {
		t.Fatalf("first Stop reason = %q; want presentation file path %q", out.Reason, expectedPath)
	}

	// Second Stop with missing receipt token in transcript keeps the presentation unacknowledged.
	writeAGYTranscript(t, transcriptPath, []string{
		"Initial turn completed before delivery.",
		"I processed the task but forgot the receipt token.",
	})
	out, err = Stop(context.Background(), home, HookInput{
		ConversationID:    "conv-1",
		WorkspacePaths:    []string{project},
		TranscriptPath:    transcriptPath,
		TerminationReason: "NO_TOOL_CALL",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != "continue" || !strings.Contains(out.Reason, shown.Token) {
		t.Fatalf("second Stop without token = %+v; want re-presentation of %q", out, shown.Token)
	}
	if stillPending, err := loadPresentation(home, "planner"); err != nil || stillPending == nil {
		t.Fatalf("presentation after missing token = %#v, %v; want still pending", stillPending, err)
	}

	// Third Stop where the AGY transcript PLANNER_RESPONSE ends with the receipt token acknowledges it.
	writeAGYTranscript(t, transcriptPath, []string{
		"Initial turn completed before delivery.",
		"Processed the task and published reply.\n" + shown.Token + "\n",
	})
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stopCancel()
	out, err = Stop(stopCtx, home, HookInput{
		ConversationID:    "conv-1",
		WorkspacePaths:    []string{project},
		TranscriptPath:    transcriptPath,
		TerminationReason: "NO_TOOL_CALL",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != "" || out.Reason != "" {
		t.Fatalf("third Stop after receipt ack = %+v; want empty allow-stop", out)
	}
	if again, err := loadPresentation(home, "planner"); err != nil || again != nil {
		t.Fatalf("presentation after ack = %#v, %v; want nil", again, err)
	}

	// Verify the bus cursor actually advanced so delta read is now empty.
	delta, err := agent.Get(state.ReadRequest{Topic: "dev/tasks", Mode: state.ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Publications) != 0 {
		t.Fatalf("delta after receipt ack = %#v; want empty", delta.Publications)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reside did not exit after context cancel")
	}
}

func TestRewriteHookCommandSubstitutesOSBinary(t *testing.T) {
	got := string(RewriteHookCommand("hooks.json", []byte(`"command": "${CONDUCTOR_BIN} adapter agy stop --hook"`)))
	if strings.Contains(got, "${CONDUCTOR_BIN}") {
		t.Fatalf("placeholder was not rewritten: %s", got)
	}
	if os.PathSeparator == '\\' && !strings.Contains(got, `bin\\conductor.exe`) {
		t.Fatalf("windows command = %s; want bin\\\\conductor.exe", got)
	}
	if string(RewriteHookCommand("README.md", []byte("${CONDUCTOR_BIN}"))) != "${CONDUCTOR_BIN}" {
		t.Fatal("rewrite touched a file other than hooks.json")
	}
}

func TestAnnouncementFormatsBoundAndUnboundSessions(t *testing.T) {
	unbound := Announcement("/state", "conv-1", "/work", Resolution{})
	if !strings.Contains(unbound, "no agent identity is bound yet") || !strings.Contains(unbound, "conductor adapter agy bind") {
		t.Fatalf("unbound announcement = %q", unbound)
	}
	bound := Announcement("/state", "conv-1", "/work", Resolution{Session: "bob", Project: "alice"})
	if !strings.Contains(bound, `acting as "bob"`) || !strings.Contains(bound, `overriding project .conductor-agent "alice"`) {
		t.Fatalf("bound announcement = %q", bound)
	}
}

func TestHookSessionRejectsEnvFallbackWithoutStdinOrExplicitFlag(t *testing.T) {
	t.Setenv(SessionEnvironment, "forged-from-env")
	var empty HookInput
	if _, err := empty.HookSession(""); err == nil {
		t.Fatal("HookSession succeeded from environment fallback without stdin conversationId or explicit --session")
	}
	got, err := (HookInput{ConversationID: "from-stdin"}).HookSession("")
	if err != nil || got != "from-stdin" {
		t.Fatalf("HookSession with stdin = %q, %v; want from-stdin, nil", got, err)
	}
}

func TestStopContentionAndTwoStepTakeover(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, IdentityFile), []byte("planner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner, err := state.New(home, "")
	if err != nil {
		t.Fatal(err)
	}
	home = owner.Home
	if _, err := owner.Join("planner", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Join("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	agent, err := state.Open(home, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	drain(t, agent)
	if _, err := owner.Put("dev/tasks", "contested message"); err != nil {
		t.Fatal(err)
	}
	watchCtx, watchCancel := context.WithTimeout(context.Background(), time.Second)
	defer watchCancel()
	batch, err := agent.WatchContext(watchCtx)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := state.IssueReceipt(agent.Agent, batch)
	if err != nil {
		t.Fatal(err)
	}
	token, err := receiptToken(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePresentation(home, "planner", presentation{
		Kind:     "delivery",
		Phase:    "delivered",
		Session:  "conv-1",
		Token:    token,
		Receipt:  receipt,
		OwnerPID: os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}

	// Second session conv-2 hits Stop while conv-1's owner PID is alive:
	// Stop records Claimant="conv-2" and returns empty StopOutput without hijacking.
	out, err := Stop(context.Background(), home, HookInput{
		ConversationID:    "conv-2",
		WorkspacePaths:    []string{project},
		TerminationReason: "NO_TOOL_CALL",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != "" || out.Reason != "" {
		t.Fatalf("Stop from contending session = %+v; want empty StopOutput", out)
	}
	shown, err := loadPresentation(home, "planner")
	if err != nil || shown == nil || shown.Session != "conv-1" || shown.Claimant != "conv-2" {
		t.Fatalf("presentation after contention = %#v, %v; want Session=conv-1, Claimant=conv-2", shown, err)
	}

	// Step 1 of Takeover issues a deterministic confirmation token without transferring ownership.
	challenge, err := Takeover(home, "planner", "")
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Status != "confirmation-required" || challenge.Confirm == "" {
		t.Fatalf("Takeover step 1 = %+v; want confirmation-required", challenge)
	}

	// Step 2 of Takeover with the confirmation token transfers presentation ownership to conv-2 without acknowledging.
	confirmed, err := Takeover(home, "planner", challenge.Confirm)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != "released" {
		t.Fatalf("Takeover step 2 = %+v; want released", confirmed)
	}
	shown, err = loadPresentation(home, "planner")
	if err != nil || shown == nil || shown.Session != "conv-2" || shown.Claimant != "" {
		t.Fatalf("presentation after takeover = %#v, %v; want Session=conv-2, Claimant empty", shown, err)
	}
}

func writeAGYTranscript(t *testing.T, path string, plannerResponses []string) {
	t.Helper()
	var buf strings.Builder
	stepIdx := 0
	for _, content := range plannerResponses {
		userStep, _ := json.Marshal(map[string]any{
			"step_index": stepIdx,
			"source":     "USER_EXPLICIT",
			"type":       "USER_INPUT",
			"status":     "DONE",
			"content":    "user prompt",
		})
		buf.Write(userStep)
		buf.WriteByte('\n')
		stepIdx++

		modelStep, _ := json.Marshal(map[string]any{
			"step_index": stepIdx,
			"source":     "MODEL",
			"type":       "PLANNER_RESPONSE",
			"status":     "DONE",
			"content":    content,
		})
		buf.Write(modelStep)
		buf.WriteByte('\n')
		stepIdx++
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func drain(t *testing.T, client *state.Client) {
	t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		batch, err := client.WatchContext(ctx)
		cancel()
		if err != nil || len(batch.Deliveries) == 0 {
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

package codex

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	conductor "github.com/danlavee/Conductor"
)

type stubWatchClient struct {
	signals      []conductor.Signal
	watchIndex   int
	acknowledged []conductor.Signal
	watchErr     error
	ackErr       error
}

func (c *stubWatchClient) WatchContext(context.Context) (conductor.Signal, error) {
	if c.watchIndex >= len(c.signals) {
		return conductor.Signal{}, c.watchErr
	}
	signal := c.signals[c.watchIndex]
	c.watchIndex++
	return signal, nil
}

func (c *stubWatchClient) AcknowledgeSignal(signal conductor.Signal) error {
	if c.ackErr != nil {
		return c.ackErr
	}
	c.acknowledged = append(c.acknowledged, signal)
	return nil
}

type stubActivator struct {
	threadID string
	agent    string
	signals  []conductor.Signal
	err      error
}

func (a *stubActivator) Activate(_ context.Context, threadID, agent string, signal conductor.Signal) error {
	a.threadID = threadID
	a.agent = agent
	a.signals = append(a.signals, signal)
	return a.err
}

func TestRunDeliversThenAcknowledges(t *testing.T) {
	stop := errors.New("stop")
	signal := conductor.Signal{Type: "update", Resource: "dev/tasks", Key: "task", Index: 7, Agent: "publisher"}
	client := &stubWatchClient{signals: []conductor.Signal{signal}, watchErr: stop}
	activator := &stubActivator{}

	err := Run(context.Background(), client, activator, "thread-1", "tester1")
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want stop", err)
	}
	if activator.threadID != "thread-1" || activator.agent != "tester1" || len(activator.signals) != 1 || activator.signals[0] != signal {
		t.Fatalf("activation = %#v", activator)
	}
	if len(client.acknowledged) != 1 || client.acknowledged[0] != signal {
		t.Fatalf("acknowledged = %#v", client.acknowledged)
	}
}

func TestRunLeavesFailedDeliveryUnread(t *testing.T) {
	signal := conductor.Signal{Type: "join", Resource: "registry", Key: "reviewer", Index: 3, Agent: "reviewer"}
	client := &stubWatchClient{signals: []conductor.Signal{signal}}
	activator := &stubActivator{err: errors.New("delivery failed")}

	err := Run(context.Background(), client, activator, "thread-1", "tester1")
	if err == nil || !strings.Contains(err.Error(), "signal 3") {
		t.Fatalf("error = %v", err)
	}
	if len(client.acknowledged) != 0 {
		t.Fatalf("failed delivery was acknowledged: %#v", client.acknowledged)
	}
}

func TestRunReportsAcknowledgementFailure(t *testing.T) {
	signal := conductor.Signal{Type: "update", Resource: "dev/tasks", Key: "task", Index: 4, Agent: "publisher"}
	client := &stubWatchClient{signals: []conductor.Signal{signal}, ackErr: errors.New("checkpoint failed")}
	activator := &stubActivator{}

	err := Run(context.Background(), client, activator, "thread-1", "tester1")
	if err == nil || !strings.Contains(err.Error(), "after Codex delivery") {
		t.Fatalf("error = %v", err)
	}
	if len(activator.signals) != 1 {
		t.Fatalf("activation count = %d", len(activator.signals))
	}
}

func TestRunRequiresThreadAndAgent(t *testing.T) {
	client := &stubWatchClient{}
	activator := &stubActivator{}
	if err := Run(context.Background(), client, activator, "", "tester1"); err == nil || !strings.Contains(err.Error(), ThreadEnvironment) {
		t.Fatalf("missing thread error = %v", err)
	}
	if err := Run(context.Background(), client, activator, "thread-1", ""); err == nil || !strings.Contains(err.Error(), "agent name") {
		t.Fatalf("missing agent error = %v", err)
	}
}

func TestSignalPromptDefinesAdapterOwnership(t *testing.T) {
	signal := conductor.Signal{Type: "leave", Resource: "registry", Key: "reviewer", Index: 9, Agent: "reviewer"}
	prompt, err := SignalPrompt("tester1", signal)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"index":9`, `"type":"leave"`, "refresh the roster", "do not start conductor watch", "idempotently"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("prompt omits %q: %s", fragment, prompt)
		}
	}
}

func TestResumeArgumentsAllowNonGitWorkspace(t *testing.T) {
	arguments := ResumeArguments("thread-1", "process signal", "danger-full-access")
	want := []string{"exec", "--skip-git-repo-check", "--json", "--sandbox", "danger-full-access", "resume", "thread-1", "process signal"}
	if strings.Join(arguments, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments = %#v", arguments)
	}
}

func TestSandboxPrefersExplicitAdapterSetting(t *testing.T) {
	if got := Sandbox(map[string]string{SandboxEnvironment: "workspace-write", PermissionEnvironment: ":danger-full-access"}); got != "workspace-write" {
		t.Fatalf("sandbox = %q", got)
	}
	if got := Sandbox(map[string]string{PermissionEnvironment: ":danger-full-access"}); got != ":danger-full-access" {
		t.Fatalf("fallback sandbox = %q", got)
	}
}

func TestNewRejectsUnknownSandbox(t *testing.T) {
	if _, err := New("codex", "unbounded", nil, nil); err == nil || !strings.Contains(err.Error(), SandboxEnvironment) {
		t.Fatalf("error = %v", err)
	}
}

func TestObserveEventsRequiresSuccessfulCompletion(t *testing.T) {
	for name, test := range map[string]struct {
		input         string
		wantCompleted bool
		wantError     bool
	}{
		"completed": {input: "{\"type\":\"turn.started\"}\n{\"type\":\"turn.completed\"}\n", wantCompleted: true},
		"failed":    {input: "{\"type\":\"turn.failed\"}\n", wantError: true},
		"missing":   {input: "{\"type\":\"turn.started\"}\n"},
		"malformed": {input: "not-json\n", wantError: true},
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			completed, err := ObserveEvents(strings.NewReader(test.input), &output)
			if completed != test.wantCompleted || (err != nil) != test.wantError {
				t.Fatalf("completed=%v error=%v", completed, err)
			}
			if output.String() != test.input {
				t.Fatalf("forwarded output = %q", output.String())
			}
		})
	}
}

func TestSetEnvironmentReplacesValuesCaseInsensitively(t *testing.T) {
	environment := setEnvironment([]string{"Path=one", "CONDUCTOR_AGENT=old", "OTHER=value"}, map[string]string{
		"CONDUCTOR_AGENT": "new",
		"CODEX_THREAD_ID": "thread-1",
	})
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "CONDUCTOR_AGENT=old") || !strings.Contains(joined, "CONDUCTOR_AGENT=new") || !strings.Contains(joined, "CODEX_THREAD_ID=thread-1") {
		t.Fatalf("environment = %q", joined)
	}
}

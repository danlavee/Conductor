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
	summaries    []conductor.Summary
	watchIndex   int
	acknowledged []conductor.Delivery
	watchErr     error
	ackErr       error
}

func (c *stubWatchClient) WatchContext(context.Context) (conductor.Summary, error) {
	if c.watchIndex >= len(c.summaries) {
		return conductor.Summary{}, c.watchErr
	}
	summary := c.summaries[c.watchIndex]
	c.watchIndex++
	return summary, nil
}

func (c *stubWatchClient) ResolveDelivery(summary conductor.Summary, mode conductor.DeliveryMode) (conductor.Delivery, error) {
	return conductor.Delivery{Summary: summary, Mode: mode}, nil
}

func (c *stubWatchClient) AcknowledgeDelivery(delivery conductor.Delivery) error {
	if c.ackErr != nil {
		return c.ackErr
	}
	c.acknowledged = append(c.acknowledged, delivery)
	return nil
}

type stubActivator struct {
	threadID   string
	agent      string
	deliveries []conductor.Delivery
	err        error
}

func (a *stubActivator) Activate(_ context.Context, threadID, agent string, delivery conductor.Delivery) error {
	a.threadID = threadID
	a.agent = agent
	a.deliveries = append(a.deliveries, delivery)
	return a.err
}

func TestRunDeliversThenAcknowledges(t *testing.T) {
	stop := errors.New("stop")
	summary := conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 7, Agent: "publisher"}
	client := &stubWatchClient{summaries: []conductor.Summary{summary}, watchErr: stop}
	activator := &stubActivator{}

	err := Run(context.Background(), client, activator, "thread-1", "tester1", conductor.DeliveryContent)
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want stop", err)
	}
	if activator.threadID != "thread-1" || activator.agent != "tester1" || len(activator.deliveries) != 1 || activator.deliveries[0].Summary != summary || activator.deliveries[0].Mode != conductor.DeliveryContent {
		t.Fatalf("activation = %#v", activator)
	}
	if len(client.acknowledged) != 1 || client.acknowledged[0].Summary != summary {
		t.Fatalf("acknowledged = %#v", client.acknowledged)
	}
}

func TestRunLeavesFailedDeliveryUnread(t *testing.T) {
	summary := conductor.Summary{Type: "join", Topic: "registry", Sequence: 3, Agent: "reviewer"}
	client := &stubWatchClient{summaries: []conductor.Summary{summary}}
	activator := &stubActivator{err: errors.New("delivery failed")}

	err := Run(context.Background(), client, activator, "thread-1", "tester1", conductor.DeliveryContent)
	if err == nil || !strings.Contains(err.Error(), "summary 3") {
		t.Fatalf("error = %v", err)
	}
	if len(client.acknowledged) != 0 {
		t.Fatalf("failed delivery was acknowledged: %#v", client.acknowledged)
	}
}

func TestRunReportsAcknowledgementFailure(t *testing.T) {
	summary := conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 4, Agent: "publisher"}
	client := &stubWatchClient{summaries: []conductor.Summary{summary}, ackErr: errors.New("checkpoint failed")}
	activator := &stubActivator{}

	err := Run(context.Background(), client, activator, "thread-1", "tester1", conductor.DeliveryContent)
	if err == nil || !strings.Contains(err.Error(), "after Codex delivery") {
		t.Fatalf("error = %v", err)
	}
	if len(activator.deliveries) != 1 {
		t.Fatalf("activation count = %d", len(activator.deliveries))
	}
}

func TestRunRequiresThreadAndAgent(t *testing.T) {
	client := &stubWatchClient{}
	activator := &stubActivator{}
	if err := Run(context.Background(), client, activator, "", "tester1", conductor.DeliveryContent); err == nil || !strings.Contains(err.Error(), "thread ID") {
		t.Fatalf("missing thread error = %v", err)
	}
	if err := Run(context.Background(), client, activator, "thread-1", "", conductor.DeliveryContent); err == nil || !strings.Contains(err.Error(), "agent name") {
		t.Fatalf("missing agent error = %v", err)
	}
}

func TestSignalPromptDefinesAdapterOwnership(t *testing.T) {
	delivery := conductor.Delivery{Summary: conductor.Summary{Type: "leave", Topic: "registry", Sequence: 9, Agent: "reviewer"}, Mode: conductor.DeliveryContent, Roster: []conductor.Agent{{Name: "tester1"}}}
	prompt, err := SignalPrompt("tester1", delivery)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"sequence":9`, `"type":"leave"`, `"mode":"content"`, "complete topic delta or roster", "idempotently"} {
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

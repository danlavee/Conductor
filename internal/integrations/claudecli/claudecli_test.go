package claudecli

import (
	"context"
	"errors"
	"strings"
	"testing"

	conductor "github.com/danlavee/Conductor"
)

type stubWatchClient struct {
	summary      conductor.Summary
	delivered    bool
	acknowledged bool
	stop         error
	resolveErr   error
}

func (c *stubWatchClient) WatchResultContext(context.Context) (conductor.WatchResult, error) {
	if c.delivered {
		return conductor.WatchResult{}, c.stop
	}
	c.delivered = true
	return conductor.WatchResult{Summaries: []conductor.Summary{c.summary}}, nil
}

func (c *stubWatchClient) ResolveDelivery(summary conductor.Summary, mode conductor.DeliveryMode) (conductor.Delivery, error) {
	if c.resolveErr != nil {
		return conductor.Delivery{}, c.resolveErr
	}
	return conductor.Delivery{Summary: summary, Mode: mode}, nil
}

func (c *stubWatchClient) AcknowledgeDelivery(conductor.Delivery) error {
	c.acknowledged = true
	return nil
}

type stubActivator struct {
	session  string
	agent    string
	delivery conductor.Delivery
	err      error
}

func (a *stubActivator) Activate(_ context.Context, session, agent string, delivery conductor.Delivery) error {
	a.session = session
	a.agent = agent
	a.delivery = delivery
	return a.err
}

func (a *stubActivator) ActivateReplacement(context.Context, string, string, conductor.ReplacementActivation) error {
	return a.err
}

func TestRunDeliversThenAcknowledges(t *testing.T) {
	stop := errors.New("stop")
	client := &stubWatchClient{summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 4, Agent: "writer"}, stop: stop}
	activator := &stubActivator{}
	err := Run(context.Background(), client, activator, "session-1", "tester1", conductor.DeliverySummary)
	if !errors.Is(err, stop) || !client.acknowledged || activator.session != "session-1" || activator.agent != "tester1" {
		t.Fatalf("error=%v acknowledged=%v activator=%#v", err, client.acknowledged, activator)
	}
}

func TestRunLeavesFailedDeliveryUnread(t *testing.T) {
	client := &stubWatchClient{summary: conductor.Summary{Type: "join", Topic: "registry", Sequence: 5, Agent: "writer"}}
	err := Run(context.Background(), client, &stubActivator{err: errors.New("failed")}, "session-1", "tester1", conductor.DeliverySummary)
	if err == nil || client.acknowledged || !strings.Contains(err.Error(), "summary 5") {
		t.Fatalf("error=%v acknowledged=%v", err, client.acknowledged)
	}
}

func TestRunPassesModeThroughToResolveAndActivator(t *testing.T) {
	stop := errors.New("stop")
	client := &stubWatchClient{summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 4, Agent: "writer"}, stop: stop}
	activator := &stubActivator{}
	err := Run(context.Background(), client, activator, "session-1", "tester1", conductor.DeliveryContent)
	if !errors.Is(err, stop) || activator.delivery.Mode != conductor.DeliveryContent {
		t.Fatalf("error=%v delivery=%#v", err, activator.delivery)
	}
}

func TestResumeArgumentsAndPrompt(t *testing.T) {
	want := []string{"--print", "--output-format", "json", "--resume", "session-1", "process signal"}
	if got := ResumeArguments("session-1", "process signal"); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments = %#v", got)
	}
	prompt, err := SignalPrompt("tester1", conductor.Delivery{Summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 8, Agent: "writer"}, Mode: conductor.DeliverySummary})
	if err != nil || !strings.Contains(prompt, `"sequence":8`) || !strings.Contains(prompt, "do not start conductor watch") {
		t.Fatalf("prompt=%q error=%v", prompt, err)
	}
}

func TestValidateRefusesSelfTargetingTheLiveSession(t *testing.T) {
	err := Validate("session-1", "tester1", "session-1")
	if err == nil {
		t.Fatal("expected an error when the target session equals the live session")
	}
	for _, want := range []string{"session-1", "live session", "watch", "--claude-cli"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestValidateAllowsDifferentTargetSession(t *testing.T) {
	if err := Validate("session-1", "tester1", "session-2"); err != nil {
		t.Fatalf("unexpected error targeting a different session: %v", err)
	}
}

func TestValidateAllowsEmptyLiveSession(t *testing.T) {
	if err := Validate("session-1", "tester1", ""); err != nil {
		t.Fatalf("unexpected error with no live session set: %v", err)
	}
}

func TestValidateRequiresSessionAndAgent(t *testing.T) {
	if err := Validate("", "tester1", ""); err == nil || !strings.Contains(err.Error(), "session ID") {
		t.Fatalf("missing session error = %v", err)
	}
	if err := Validate("session-1", "", ""); err == nil || !strings.Contains(err.Error(), "agent name") {
		t.Fatalf("missing agent error = %v", err)
	}
}

func TestSignalPromptContentModeSkipsGetAndListAgents(t *testing.T) {
	prompt, err := SignalPrompt("tester1", conductor.Delivery{
		Summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 8, Agent: "writer"},
		Mode:    conductor.DeliveryContent,
		Delta:   &conductor.ReadResult{Mode: "delta", Topic: "dev/tasks"},
	})
	if err != nil || !strings.Contains(prompt, "already included") || !strings.Contains(prompt, "Do not call get, list-agents, or watch") || !strings.Contains(prompt, "after this turn succeeds") {
		t.Fatalf("prompt=%q error=%v", prompt, err)
	}
}

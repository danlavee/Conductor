package agydesktop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	conductor "github.com/danlavee/Conductor"
)

type stubWatchClient struct {
	summary      conductor.Summary
	delivered    bool
	acknowledged bool
	stop         error
}

func (c *stubWatchClient) WatchContext(context.Context) (conductor.Summary, error) {
	if c.delivered {
		return conductor.Summary{}, c.stop
	}
	c.delivered = true
	return c.summary, nil
}

func (c *stubWatchClient) ResolveDelivery(summary conductor.Summary, mode conductor.DeliveryMode) (conductor.Delivery, error) {
	return conductor.Delivery{Summary: summary, Mode: mode}, nil
}

func (c *stubWatchClient) AcknowledgeDelivery(conductor.Delivery) error {
	c.acknowledged = true
	return nil
}

type stubActivator struct {
	conversation string
	agent        string
	err          error
}

func (a *stubActivator) Check(context.Context, string) error { return nil }

func (a *stubActivator) Activate(_ context.Context, conversation, agent string, _ conductor.Delivery) error {
	a.conversation = conversation
	a.agent = agent
	return a.err
}

func TestRunDeliversThenAcknowledges(t *testing.T) {
	stop := errors.New("stop")
	client := &stubWatchClient{summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 4, Agent: "writer"}, stop: stop}
	activator := &stubActivator{}
	err := Run(context.Background(), client, activator, "conversation-1", "tester1", conductor.DeliveryContent)
	if !errors.Is(err, stop) || !client.acknowledged || activator.conversation != "conversation-1" || activator.agent != "tester1" {
		t.Fatalf("error=%v acknowledged=%v activator=%#v", err, client.acknowledged, activator)
	}
}

func TestRunLeavesFailedDeliveryUnread(t *testing.T) {
	client := &stubWatchClient{summary: conductor.Summary{Type: "join", Topic: "registry", Sequence: 5, Agent: "writer"}}
	err := Run(context.Background(), client, &stubActivator{err: errors.New("failed")}, "conversation-1", "tester1", conductor.DeliveryContent)
	if err == nil || client.acknowledged || !strings.Contains(err.Error(), "summary 5") {
		t.Fatalf("error=%v acknowledged=%v", err, client.acknowledged)
	}
}

func TestEnvironmentAndPrompt(t *testing.T) {
	if err := Validate("conversation-1", "tester1"); err != nil {
		t.Fatal(err)
	}
	prompt, err := SignalPrompt("tester1", conductor.Delivery{Summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 8, Agent: "writer"}, Mode: conductor.DeliveryContent})
	if err != nil || !strings.Contains(prompt, `"sequence":8`) || !strings.Contains(prompt, `"mode":"content"`) || !strings.Contains(prompt, "complete topic delta or roster") {
		t.Fatalf("prompt=%q error=%v", prompt, err)
	}
}

func TestAgentAPIValidatesThenSendsMessage(t *testing.T) {
	t.Setenv("GO_WANT_AGENTAPI_HELPER", "1")
	api := &AgentAPI{
		executable: "agentapi-test",
		command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			arguments := append([]string{"-test.run=TestAgentAPIHelperProcess", "--"}, args...)
			return exec.CommandContext(ctx, os.Args[0], arguments...)
		},
	}
	if err := api.Check(context.Background(), "conversation-1"); err != nil {
		t.Fatal(err)
	}
	if err := api.Activate(context.Background(), "conversation-1", "tester1", conductor.Delivery{Summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 9, Agent: "writer"}, Mode: conductor.DeliveryContent}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentAPIHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AGENTAPI_HELPER") != "1" {
		return
	}
	separator := 0
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index + 1
			break
		}
	}
	arguments := os.Args[separator:]
	if len(arguments) == 2 && arguments[0] == "get-conversation-metadata" && arguments[1] == "conversation-1" {
		os.Exit(0)
	}
	if len(arguments) == 3 && arguments[0] == "send-message" && arguments[1] == "conversation-1" && strings.Contains(arguments[2], `"sequence":9`) {
		os.Exit(0)
	}
	os.Exit(2)
}

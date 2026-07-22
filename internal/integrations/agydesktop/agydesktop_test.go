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
	signal       conductor.Signal
	delivered    bool
	acknowledged bool
	stop         error
}

func (c *stubWatchClient) WatchContext(context.Context) (conductor.Signal, error) {
	if c.delivered {
		return conductor.Signal{}, c.stop
	}
	c.delivered = true
	return c.signal, nil
}

func (c *stubWatchClient) AcknowledgeSignal(conductor.Signal) error {
	c.acknowledged = true
	return nil
}

type stubActivator struct {
	conversation string
	agent        string
	err          error
}

func (a *stubActivator) Check(context.Context, string) error { return nil }

func (a *stubActivator) Activate(_ context.Context, conversation, agent string, _ conductor.Signal) error {
	a.conversation = conversation
	a.agent = agent
	return a.err
}

func TestRunDeliversThenAcknowledges(t *testing.T) {
	stop := errors.New("stop")
	client := &stubWatchClient{signal: conductor.Signal{Type: "update", Resource: "dev/tasks", Index: 4}, stop: stop}
	activator := &stubActivator{}
	err := Run(context.Background(), client, activator, "conversation-1", "tester1")
	if !errors.Is(err, stop) || !client.acknowledged || activator.conversation != "conversation-1" || activator.agent != "tester1" {
		t.Fatalf("error=%v acknowledged=%v activator=%#v", err, client.acknowledged, activator)
	}
}

func TestRunLeavesFailedDeliveryUnread(t *testing.T) {
	client := &stubWatchClient{signal: conductor.Signal{Type: "join", Resource: "registry", Index: 5}}
	err := Run(context.Background(), client, &stubActivator{err: errors.New("failed")}, "conversation-1", "tester1")
	if err == nil || client.acknowledged || !strings.Contains(err.Error(), "signal 5") {
		t.Fatalf("error=%v acknowledged=%v", err, client.acknowledged)
	}
}

func TestEnvironmentAndPrompt(t *testing.T) {
	environment := EnvironmentFrom(func(key string) string {
		return map[string]string{
			BinaryEnvironment: "agentapi-test", ConversationEnvironment: "conversation-1",
		}[key]
	})
	if environment.Executable != "agentapi-test" || environment.ConversationID != "conversation-1" || environment.Validate("tester1") != nil {
		t.Fatalf("environment=%+v", environment)
	}
	prompt, err := SignalPrompt("tester1", conductor.Signal{Type: "update", Resource: "dev/tasks", Index: 8})
	if err != nil || !strings.Contains(prompt, `"index":8`) || !strings.Contains(prompt, "CONDUCTOR_AGENT=tester1") || !strings.Contains(prompt, "do not start conductor watch") {
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
	if err := api.Activate(context.Background(), "conversation-1", "tester1", conductor.Signal{Type: "update", Resource: "dev/tasks", Index: 9}); err != nil {
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
	if len(arguments) == 3 && arguments[0] == "send-message" && arguments[1] == "conversation-1" && strings.Contains(arguments[2], `"index":9`) {
		os.Exit(0)
	}
	os.Exit(2)
}

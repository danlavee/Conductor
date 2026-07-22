package agycli

import (
	"bytes"
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

func TestEnvironmentValidation(t *testing.T) {
	values := map[string]string{BinaryEnvironment: "agy-test", ConversationEnvironment: "conversation-1"}
	environment := EnvironmentFrom(func(key string) string { return values[key] })
	if environment.Executable != "agy-test" || environment.ConversationID != "conversation-1" {
		t.Fatalf("environment = %#v", environment)
	}
	if err := environment.Validate("tester1"); err != nil {
		t.Fatal(err)
	}
	if err := (Environment{}).Validate("tester1"); err == nil || !strings.Contains(err.Error(), ConversationEnvironment) {
		t.Fatalf("missing conversation error = %v", err)
	}
	if err := environment.Validate(" "); err == nil || !strings.Contains(err.Error(), "agent name") {
		t.Fatalf("missing agent error = %v", err)
	}
}

func TestResumeArgumentsAndPrompt(t *testing.T) {
	want := []string{"--print", "--conversation", "conversation-1", "process signal"}
	if got := ResumeArguments("conversation-1", "process signal"); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments = %#v", got)
	}
	prompt, err := SignalPrompt("tester1", conductor.Signal{Type: "update", Resource: "dev/tasks", Index: 8})
	if err != nil || !strings.Contains(prompt, `"index":8`) || !strings.Contains(prompt, "do not start conductor watch") || !strings.Contains(prompt, "idempotently") {
		t.Fatalf("prompt=%q error=%v", prompt, err)
	}
}

func TestActivateRequiresSuccessfulNonEmptyOutputAndSetsDeliveryEnvironment(t *testing.T) {
	for _, test := range []struct {
		name    string
		mode    string
		wantErr string
	}{
		{name: "success", mode: "success"},
		{name: "empty", mode: "empty", wantErr: "without output"},
		{name: "failure", mode: "failure", wantErr: "exit status"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GO_WANT_AGY_HELPER", "1")
			t.Setenv("GO_AGY_HELPER_MODE", test.mode)
			var forwarded bytes.Buffer
			cli := &CLI{
				executable: "agy-test",
				stdout:     &forwarded,
				stderr:     &forwarded,
				command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
					return exec.CommandContext(ctx, os.Args[0], "-test.run=TestAGYHelperProcess")
				},
			}
			err := cli.Activate(context.Background(), "conversation-1", "tester1", conductor.Signal{Type: "update", Resource: "dev/tasks", Index: 9})
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestAGYHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AGY_HELPER") != "1" {
		return
	}
	if os.Getenv("CONDUCTOR_AGENT") != "tester1" || os.Getenv(DeliveryEnvironment) != "1" || os.Getenv(ConversationEnvironment) != "conversation-1" {
		os.Exit(3)
	}
	switch os.Getenv("GO_AGY_HELPER_MODE") {
	case "success":
		_, _ = os.Stdout.WriteString("processed\n")
		os.Exit(0)
	case "empty":
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

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
	delivery     conductor.Delivery
	err          error
}

func (a *stubActivator) Activate(_ context.Context, conversation, agent string, delivery conductor.Delivery) error {
	a.conversation = conversation
	a.agent = agent
	a.delivery = delivery
	return a.err
}

func TestRunDeliversThenAcknowledges(t *testing.T) {
	stop := errors.New("stop")
	client := &stubWatchClient{summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 4, Agent: "writer"}, stop: stop}
	activator := &stubActivator{}
	err := Run(context.Background(), client, activator, "conversation-1", "tester1", conductor.DeliverySummary)
	if !errors.Is(err, stop) || !client.acknowledged || activator.conversation != "conversation-1" || activator.agent != "tester1" {
		t.Fatalf("error=%v acknowledged=%v activator=%#v", err, client.acknowledged, activator)
	}
}

func TestRunLeavesFailedDeliveryUnread(t *testing.T) {
	client := &stubWatchClient{summary: conductor.Summary{Type: "join", Topic: "registry", Sequence: 5, Agent: "writer"}}
	err := Run(context.Background(), client, &stubActivator{err: errors.New("failed")}, "conversation-1", "tester1", conductor.DeliverySummary)
	if err == nil || client.acknowledged || !strings.Contains(err.Error(), "summary 5") {
		t.Fatalf("error=%v acknowledged=%v", err, client.acknowledged)
	}
}

func TestEnvironmentValidation(t *testing.T) {
	if err := Validate("conversation-1", "tester1"); err != nil {
		t.Fatal(err)
	}
	if err := Validate("", "tester1"); err == nil || !strings.Contains(err.Error(), "conversation ID") {
		t.Fatalf("missing conversation error = %v", err)
	}
	if err := Validate("conversation-1", " "); err == nil || !strings.Contains(err.Error(), "agent name") {
		t.Fatalf("missing agent error = %v", err)
	}
}

func TestResumeArgumentsAndPrompt(t *testing.T) {
	want := []string{"--print", "--conversation", "conversation-1", "process signal"}
	if got := ResumeArguments("conversation-1", "process signal"); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments = %#v", got)
	}
	prompt, err := SignalPrompt("tester1", conductor.Delivery{Summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 8, Agent: "writer"}, Mode: conductor.DeliverySummary})
	if err != nil || !strings.Contains(prompt, `"sequence":8`) || !strings.Contains(prompt, "do not start conductor watch") || !strings.Contains(prompt, "idempotently") {
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
				Transport:  transport,
				Executable: "agy-test",
				Stdout:     &forwarded,
				Stderr:     &forwarded,
				Command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
					return exec.CommandContext(ctx, os.Args[0], "-test.run=TestAGYHelperProcess")
				},
			}
			err := cli.Activate(context.Background(), "conversation-1", "tester1", conductor.Delivery{Summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 9, Agent: "writer"}, Mode: conductor.DeliverySummary})
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
			if test.name == "failure" {
				want := "use 'conductor tester1 watch --agy <conversation-id>' instead of '--agy-cli'"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain the --agy hint %q", err, want)
				}
			}
		})
	}
}

func TestAGYHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AGY_HELPER") != "1" {
		return
	}
	if os.Getenv(DeliveryEnvironment) != "1" {
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

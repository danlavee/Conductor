package claudecli

import (
	"context"
	"errors"
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
	session string
	agent   string
	err     error
}

func (a *stubActivator) Activate(_ context.Context, session, agent string, _ conductor.Signal) error {
	a.session = session
	a.agent = agent
	return a.err
}

func TestRunDeliversThenAcknowledges(t *testing.T) {
	stop := errors.New("stop")
	client := &stubWatchClient{signal: conductor.Signal{Type: "update", Resource: "dev/tasks", Index: 4}, stop: stop}
	activator := &stubActivator{}
	err := Run(context.Background(), client, activator, "session-1", "tester1")
	if !errors.Is(err, stop) || !client.acknowledged || activator.session != "session-1" || activator.agent != "tester1" {
		t.Fatalf("error=%v acknowledged=%v activator=%#v", err, client.acknowledged, activator)
	}
}

func TestRunLeavesFailedDeliveryUnread(t *testing.T) {
	client := &stubWatchClient{signal: conductor.Signal{Type: "join", Resource: "registry", Index: 5}}
	err := Run(context.Background(), client, &stubActivator{err: errors.New("failed")}, "session-1", "tester1")
	if err == nil || client.acknowledged || !strings.Contains(err.Error(), "signal 5") {
		t.Fatalf("error=%v acknowledged=%v", err, client.acknowledged)
	}
}

func TestResumeArgumentsAndPrompt(t *testing.T) {
	want := []string{"--print", "--output-format", "json", "--resume", "session-1", "process signal"}
	if got := ResumeArguments("session-1", "process signal"); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments = %#v", got)
	}
	prompt, err := SignalPrompt("tester1", conductor.Signal{Type: "update", Resource: "dev/tasks", Index: 8})
	if err != nil || !strings.Contains(prompt, `"index":8`) || !strings.Contains(prompt, "do not start conductor watch") {
		t.Fatalf("prompt=%q error=%v", prompt, err)
	}
}

package cliresume

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	conductor "github.com/danlavee/Conductor"
)

// testTransport is a minimal Transport standing in for a real CLI transport,
// so cliresume's own logic can be tested without a real CLI.
var testTransport = Transport{
	RuntimeLabel:        "Test Runtime",
	TargetNoun:          "target",
	DefaultExecutable:   "test-runtime",
	DeliveryEnvironment: "CONDUCTOR_TEST_DELIVERY",
	ResumeArguments:     func(targetID, prompt string) []string { return []string{"--resume", targetID, prompt} },
	ValidateOutput:      NonEmptyOutputValidator(),
}

// testTransportWithHint is identical except it supplies a ResumeFailureHint,
// so tests can check the hint is appended when present and absent when nil.
var testTransportWithHint = func() Transport {
	transport := testTransport
	transport.ResumeFailureHint = func(targetID, agent string) string {
		return fmt.Sprintf(" (hint for %s/%s)", targetID, agent)
	}
	return transport
}()

type stubWatchClient struct {
	summary      conductor.Summary
	delivered    bool
	acknowledged bool
	stop         error
	resolveErr   error
	ackErr       error
}

func (c *stubWatchClient) WatchContext(context.Context) ([]conductor.Summary, error) {
	if c.delivered {
		return nil, c.stop
	}
	c.delivered = true
	return []conductor.Summary{c.summary}, nil
}

func (c *stubWatchClient) ResolveDelivery(summary conductor.Summary, mode conductor.DeliveryMode) (conductor.Delivery, error) {
	if c.resolveErr != nil {
		return conductor.Delivery{}, c.resolveErr
	}
	return conductor.Delivery{Summary: summary, Mode: mode}, nil
}

func (c *stubWatchClient) AcknowledgeDelivery(conductor.Delivery) error {
	c.acknowledged = true
	return c.ackErr
}

type stubActivator struct {
	target   string
	agent    string
	delivery conductor.Delivery
	err      error
}

func (a *stubActivator) Activate(_ context.Context, target, agent string, delivery conductor.Delivery) error {
	a.target = target
	a.agent = agent
	a.delivery = delivery
	return a.err
}

func TestNewFallsBackToCandidateExecutableWhenNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	candidate := filepath.Join(dir, "cliresume-candidate-tool.exe")
	if err := os.WriteFile(candidate, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	transport := testTransport
	transport.DefaultExecutable = "cliresume-not-on-path-tool"
	transport.CandidateExecutables = []string{candidate}

	cli, err := New(transport, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cli.Executable != candidate {
		t.Fatalf("executable = %q, want %q", cli.Executable, candidate)
	}
}

func TestNewFailsWithHelpfulErrorWhenNotFoundAnywhere(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	transport := testTransport
	transport.DefaultExecutable = "cliresume-not-on-path-tool"
	transport.CandidateExecutables = nil

	_, err := New(transport, "", nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{testTransport.RuntimeLabel, "PATH", "install"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestRunDeliversThenAcknowledges(t *testing.T) {
	stop := errors.New("stop")
	client := &stubWatchClient{summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 4, Agent: "writer"}, stop: stop}
	activator := &stubActivator{}
	err := Run(context.Background(), testTransport, client, activator, "target-1", "tester1", conductor.DeliverySummary)
	if !errors.Is(err, stop) || !client.acknowledged || activator.target != "target-1" || activator.agent != "tester1" {
		t.Fatalf("error=%v acknowledged=%v activator=%#v", err, client.acknowledged, activator)
	}
}

func TestRunLeavesFailedDeliveryUnread(t *testing.T) {
	client := &stubWatchClient{summary: conductor.Summary{Type: "join", Topic: "registry", Sequence: 5, Agent: "writer"}}
	err := Run(context.Background(), testTransport, client, &stubActivator{err: errors.New("failed")}, "target-1", "tester1", conductor.DeliverySummary)
	if err == nil || client.acknowledged || !strings.Contains(err.Error(), "summary 5") {
		t.Fatalf("error=%v acknowledged=%v", err, client.acknowledged)
	}
}

func TestRunPassesModeThroughToResolveAndActivator(t *testing.T) {
	stop := errors.New("stop")
	client := &stubWatchClient{summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 4, Agent: "writer"}, stop: stop}
	activator := &stubActivator{}
	err := Run(context.Background(), testTransport, client, activator, "target-1", "tester1", conductor.DeliveryContent)
	if !errors.Is(err, stop) || activator.delivery.Mode != conductor.DeliveryContent {
		t.Fatalf("error=%v delivery=%#v", err, activator.delivery)
	}
}

func TestRunValidatesTargetAndAgent(t *testing.T) {
	err := Run(context.Background(), testTransport, &stubWatchClient{}, &stubActivator{}, "", "tester1", conductor.DeliverySummary)
	if err == nil || !strings.Contains(err.Error(), "target ID") {
		t.Fatalf("missing target error = %v", err)
	}
	err = Run(context.Background(), testTransport, &stubWatchClient{}, &stubActivator{}, "target-1", " ", conductor.DeliverySummary)
	if err == nil || !strings.Contains(err.Error(), "agent name") {
		t.Fatalf("missing agent error = %v", err)
	}
}

func TestActivateSetsEnvironmentValidatesOutputAndAppendsHintOnlyWhenConfigured(t *testing.T) {
	for _, test := range []struct {
		name      string
		transport Transport
		mode      string
		wantErr   string
		wantHint  bool
	}{
		{name: "success", transport: testTransport, mode: "success"},
		{name: "empty", transport: testTransport, mode: "empty", wantErr: "without output"},
		{name: "failure-without-hint", transport: testTransport, mode: "failure", wantErr: "exit status"},
		{name: "failure-with-hint", transport: testTransportWithHint, mode: "failure", wantErr: "exit status", wantHint: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GO_WANT_CLIRESUME_HELPER", "1")
			t.Setenv("GO_CLIRESUME_HELPER_MODE", test.mode)
			var forwarded bytes.Buffer
			cli := &CLI{
				Transport:  test.transport,
				Executable: "cliresume-test",
				Stdout:     &forwarded,
				Stderr:     &forwarded,
				Command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
					return exec.CommandContext(ctx, os.Args[0], "-test.run=TestCliresumeHelperProcess")
				},
			}
			err := cli.Activate(context.Background(), "target-1", "tester1", conductor.Delivery{Summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 9, Agent: "writer"}, Mode: conductor.DeliverySummary})
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
			if hasHint := strings.Contains(err.Error(), "hint for"); hasHint != test.wantHint {
				t.Fatalf("error = %v, wantHint=%v", err, test.wantHint)
			}
		})
	}
}

func TestCliresumeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CLIRESUME_HELPER") != "1" {
		return
	}
	if os.Getenv("CONDUCTOR_TEST_DELIVERY") != "1" {
		os.Exit(3)
	}
	switch os.Getenv("GO_CLIRESUME_HELPER_MODE") {
	case "success":
		_, _ = os.Stdout.WriteString("processed\n")
		os.Exit(0)
	case "empty":
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func TestJSONOutputValidator(t *testing.T) {
	validate := JSONOutputValidator()
	if err := validate([]byte(`  {"ok":true}  `)); err != nil {
		t.Fatal(err)
	}
	if err := validate([]byte("not json")); err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("error = %v", err)
	}
	if err := validate(nil); err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("empty output error = %v", err)
	}
}

func TestNonEmptyOutputValidator(t *testing.T) {
	validate := NonEmptyOutputValidator()
	if err := validate([]byte("processed")); err != nil {
		t.Fatal(err)
	}
	if err := validate([]byte("   \n")); err == nil || !strings.Contains(err.Error(), "without output") {
		t.Fatalf("error = %v", err)
	}
	if err := validate(nil); err == nil || !strings.Contains(err.Error(), "without output") {
		t.Fatalf("nil output error = %v", err)
	}
}

func TestSignalPromptSummaryModeDefersToTheSkill(t *testing.T) {
	prompt, err := SignalPrompt("Test Runtime", "tester1", conductor.Delivery{Summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 8, Agent: "writer"}, Mode: conductor.DeliverySummary})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, `"sequence":8`) || !strings.Contains(prompt, "do not start conductor watch") {
		t.Fatalf("prompt = %q", prompt)
	}
	if strings.Contains(prompt, "Do not call get or list-agents") {
		t.Fatalf("summary prompt unexpectedly told the turn to skip get/list-agents: %q", prompt)
	}
}

func TestSignalPromptContentModeSkipsGetAndListAgents(t *testing.T) {
	prompt, err := SignalPrompt("Test Runtime", "tester1", conductor.Delivery{
		Summary: conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 8, Agent: "writer"},
		Mode:    conductor.DeliveryContent,
		Delta:   &conductor.ReadResult{Mode: "delta", Topic: "dev/tasks"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "already included") || !strings.Contains(prompt, "Do not call get or list-agents") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestSetEnvironmentOverridesCaseInsensitivelyAndPreservesOthers(t *testing.T) {
	environment := []string{"PATH=/usr/bin", "conductor_agent=old", "OTHER=keep"}
	result := setEnvironment(environment, map[string]string{"CONDUCTOR_AGENT": "new"})
	got := map[string]string{}
	for _, entry := range result {
		key, value, _ := strings.Cut(entry, "=")
		got[key] = value
	}
	if got["CONDUCTOR_AGENT"] != "new" {
		t.Fatalf("override missing: %#v", got)
	}
	if _, exists := got["conductor_agent"]; exists {
		t.Fatalf("old-case entry was not removed: %#v", got)
	}
	if got["PATH"] != "/usr/bin" || got["OTHER"] != "keep" {
		t.Fatalf("unrelated entries mutated: %#v", got)
	}
}

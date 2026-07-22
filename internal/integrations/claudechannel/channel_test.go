package claudechannel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	conductor "github.com/danlavee/Conductor"
)

type channelClient struct {
	summaries chan conductor.Summary
	acks      chan conductor.Summary
}

func (c *channelClient) WatchContext(ctx context.Context) (conductor.Summary, error) {
	select {
	case summary := <-c.summaries:
		return summary, nil
	case <-ctx.Done():
		return conductor.Summary{}, ctx.Err()
	}
}

func (c *channelClient) AcknowledgeSummary(summary conductor.Summary) error {
	c.acks <- summary
	return nil
}

func TestRunNegotiatesAndPushesSignal(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	var output bytes.Buffer
	client := &channelClient{summaries: make(chan conductor.Summary, 1), acks: make(chan conductor.Summary, 1)}
	result := make(chan error, 1)
	go func() {
		result <- Run(context.Background(), client, inputReader, &output, "tester1")
	}()

	_, _ = io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`+"\n")
	_, _ = io.WriteString(inputWriter, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	summary := conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 12, Agent: "publisher"}
	client.summaries <- summary

	select {
	case acknowledged := <-client.acks:
		if acknowledged != summary {
			t.Fatalf("acknowledged = %#v", acknowledged)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not acknowledge delivered signal")
	}
	_ = inputWriter.Close()
	if err := <-result; err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("output = %q", output.String())
	}
	var initialized map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initialized); err != nil {
		t.Fatal(err)
	}
	resultBody := initialized["result"].(map[string]any)
	capabilities := resultBody["capabilities"].(map[string]any)
	experimental := capabilities["experimental"].(map[string]any)
	if _, ok := experimental["claude/channel"]; !ok {
		t.Fatalf("initialize result = %#v", resultBody)
	}
	var notification outboundNotification
	if err := json.Unmarshal([]byte(lines[1]), &notification); err != nil {
		t.Fatal(err)
	}
	if notification.Method != "notifications/claude/channel" || notification.Params.Meta["summary_sequence"] != "12" || notification.Params.Meta["topic"] != "dev/tasks" {
		t.Fatalf("notification = %#v", notification)
	}
}

func TestRunDoesNotWatchBeforeInitialized(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	client := &channelClient{summaries: make(chan conductor.Summary), acks: make(chan conductor.Summary)}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Run(ctx, client, inputReader, io.Discard, "tester1") }()
	_, _ = io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`+"\n")
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled channel returned no error")
	}
	_ = inputWriter.Close()
}

func TestSummaryNotificationCarriesLocationNotContent(t *testing.T) {
	notification := summaryNotification("tester1", conductor.Summary{Type: "leave", Topic: "registry", Sequence: 7, Agent: "publisher"})
	if notification.Meta["summary_type"] != "leave" || notification.Meta["source_agent"] != "publisher" || !strings.Contains(notification.Content, "refresh the roster") {
		t.Fatalf("notification = %#v", notification)
	}
}

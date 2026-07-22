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
	signals chan conductor.Signal
	acks    chan conductor.Signal
}

func (c *channelClient) WatchContext(ctx context.Context) (conductor.Signal, error) {
	select {
	case signal := <-c.signals:
		return signal, nil
	case <-ctx.Done():
		return conductor.Signal{}, ctx.Err()
	}
}

func (c *channelClient) AcknowledgeSignal(signal conductor.Signal) error {
	c.acks <- signal
	return nil
}

func TestRunNegotiatesAndPushesSignal(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	var output bytes.Buffer
	client := &channelClient{signals: make(chan conductor.Signal, 1), acks: make(chan conductor.Signal, 1)}
	result := make(chan error, 1)
	go func() {
		result <- Run(context.Background(), client, inputReader, &output, "tester1")
	}()

	_, _ = io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`+"\n")
	_, _ = io.WriteString(inputWriter, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	signal := conductor.Signal{Type: "update", Resource: "dev/tasks", Key: "task", Index: 12, Agent: "publisher"}
	client.signals <- signal

	select {
	case acknowledged := <-client.acks:
		if acknowledged != signal {
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
	if notification.Method != "notifications/claude/channel" || notification.Params.Meta["signal_index"] != "12" || notification.Params.Meta["resource"] != "dev/tasks" {
		t.Fatalf("notification = %#v", notification)
	}
}

func TestRunDoesNotWatchBeforeInitialized(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	client := &channelClient{signals: make(chan conductor.Signal), acks: make(chan conductor.Signal)}
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

func TestSignalNotificationCarriesLocationNotPayload(t *testing.T) {
	notification := signalNotification("tester1", conductor.Signal{Type: "leave", Resource: "registry", Key: "agent", Index: 7, Agent: "publisher"})
	if notification.Meta["signal_type"] != "leave" || notification.Meta["publisher"] != "publisher" || !strings.Contains(notification.Content, "refresh the roster") {
		t.Fatalf("notification = %#v", notification)
	}
}

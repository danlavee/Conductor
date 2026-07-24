package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadJSONRejectsInvalidStructuredState(t *testing.T) {
	for name, data := range map[string]string{
		"trailing":   `{"name":"a","responsibility":"dev","timestamp":"2026-07-21T12:00:00Z"} {}`,
		"unknown":    `{"name":"a","responsibility":"dev","timestamp":"2026-07-21T12:00:00Z","extra":true}`,
		"incomplete": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			var agent Agent
			if err := readJSON(path, &agent); err == nil {
				t.Fatal("invalid state was accepted")
			}
		})
	}
}

func newTestClient(t *testing.T, home, agent string) *Client {
	t.Helper()
	client, err := New(home, agent)
	if err != nil {
		t.Fatal(err)
	}
	client.PollInterval = time.Millisecond
	return client
}

func assertJSONFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("invalid JSON in %s: %s", path, data)
	}
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var protocol *ProtocolError
	if !errors.As(err, &protocol) || protocol.Code != code {
		t.Fatalf("error = %v, want protocol code %s", err, code)
	}
}

// drainSummaries watches and immediately acknowledges exactly count pending
// summaries. Tests use it to skip past the collaboration/agents and join
// signals a fresh Register produces, when what they actually want to
// exercise starts afterward. A single Watch call may return more than count
// at once; anything past the requested count is left unacknowledged so it
// replays on the caller's own next Watch call, never silently over-drained.
func drainSummaries(t *testing.T, client *Client, count int) {
	t.Helper()
	drained := 0
	for drained < count {
		summaries, err := client.Watch()
		if err != nil {
			t.Fatal(err)
		}
		for _, summary := range summaries {
			if drained >= count {
				break
			}
			if err := client.AcknowledgeSummary(summary); err != nil {
				t.Fatal(err)
			}
			drained++
		}
	}
}

func watchOne(t *testing.T, client *Client) Summary {
	t.Helper()
	summaries, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("watch = %#v, want exactly one summary", summaries)
	}
	return summaries[0]
}

// drainAllPending acknowledges every summary currently pending for client,
// however many that turns out to be, so a test can reach a clean slate
// without hardcoding an assumed startup-signal count.
func drainAllPending(t *testing.T, client *Client) {
	t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		summaries, err := client.WatchContext(ctx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return
			}
			t.Fatal(err)
		}
		for _, summary := range summaries {
			if err := client.AcknowledgeSummary(summary); err != nil {
				t.Fatal(err)
			}
		}
	}
}

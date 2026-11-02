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

// joined registers name on a fresh client and subscribes it to topics.
func joined(t *testing.T, home, name string, topics ...string) *Client {
	t.Helper()
	client := newTestClient(t, home, "")
	if _, err := client.Join(name, "dev"); err != nil {
		t.Fatal(err)
	}
	for _, topic := range topics {
		if _, err := client.SubscribeTopic(topic); err != nil {
			t.Fatal(err)
		}
	}
	return client
}

// watchOne watches until a batch arrives, requires it to be one delivery, and
// acknowledges it.
func watchOne(t *testing.T, client *Client) ReadResult {
	t.Helper()
	batch, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Deliveries) != 1 {
		t.Fatalf("watch = %#v, want exactly one delivery", batch)
	}
	if err := client.AcknowledgeRead(batch.Deliveries...); err != nil {
		t.Fatal(err)
	}
	return batch.Deliveries[0]
}

// assertQuiet fails if client's watch wakes within a short wait.
func assertQuiet(t *testing.T, client *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if batch, err := client.WatchContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("watch woke: %#v, %v", batch, err)
	}
}

// creates returns n ops that each create a record with text.
func creates(n int, text string) []Op {
	ops := make([]Op, n)
	for i := range ops {
		ops[i].Text = text
	}
	return ops
}

// drainAllPending acknowledges everything currently unread for client, so a
// test can reach a clean slate without counting what joining published.
func drainAllPending(t *testing.T, client *Client) {
	t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		batch, err := client.WatchContext(ctx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err == nil {
			err = client.AcknowledgeRead(batch.Deliveries...)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

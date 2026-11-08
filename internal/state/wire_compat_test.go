package state

// Every persisted state file but history.jsonl is read through readJSON, which
// decodes strictly; history is checked here the same way. A JSON tag renamed without bumping CurrentProtocolVersion lets a
// new binary past the PROTOCOL_MISMATCH gate onto an old root, where it fails
// with a raw decode error instead -- an outage this package has had twice.
//
// Each row below decodes a checked-in fixture of a current on-disk document
// through readJSON. Renaming or removing a tag fails it, forcing the choice
// at that change: keep the field compatible, or bump CurrentProtocolVersion
// and update the fixture in the same change. Never just edit the fixture.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWireCompat(t *testing.T) {
	timestamp := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	version := 3
	for _, document := range []struct {
		file, fixture string
		decoded, want any // decoded is a pointer to the zero value of want's type
	}{{
		file: "registry/<name>.json",
		fixture: `{
  "name": "agent-a",
  "responsibility": "reviews architecture changes",
  "timestamp": "2026-07-21T12:00:00Z"
}`,
		decoded: &Agent{},
		want:    &Agent{Name: "agent-a", Responsibility: "reviews architecture changes", Timestamp: timestamp},
	}, {
		file: "one line of topics/<group>/<topic>/history.jsonl",
		fixture: `{
  "sequence": 7,
  "topic": "dev/tasks",
  "agent": "agent-a",
  "timestamp": "2026-07-21T12:00:00Z",
  "records": [
    {"index": 1, "text": "hello"}
  ]
}`,
		decoded: &Publication{},
		want:    &Publication{Sequence: 7, Topic: "dev/tasks", Agent: "agent-a", Timestamp: timestamp, Records: []Record{{Index: 1, Text: "hello"}}},
	}, {
		// The struct at the center of the incident this file exists to
		// prevent a repeat of.
		file:    "cursors/<agent>.json",
		fixture: `{"topic_sequences": {"dev/tasks": 9, "dev/tasks#2": 4}}`,
		decoded: &Cursor{},
		want:    &Cursor{Topics: map[string]int64{"dev/tasks": 9, "dev/tasks#2": 4}},
	}, {
		file:    "subscriptions/<agent>.json",
		fixture: `{"topic_groups": ["dev"], "topics": ["dev/tasks", "ops/incidents"]}`,
		decoded: &Subscription{},
		want:    &Subscription{TopicGroups: []string{"dev"}, Topics: []string{"dev/tasks", "ops/incidents"}},
	}, {
		// The compatibility gate itself: a rename here would surface as a
		// decode failure on the very file meant to produce a clean
		// PROTOCOL_MISMATCH instead.
		file:    "protocol.json",
		fixture: `{"version": 3}`,
		decoded: &protocolDocument{},
		want:    &protocolDocument{Version: &version},
	}} {
		path := filepath.Join(t.TempDir(), "fixture.json")
		if err := os.WriteFile(path, []byte(document.fixture+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := readJSON(path, document.decoded); err != nil {
			t.Fatalf("%s: readJSON: %v", document.file, err)
		}
		if !reflect.DeepEqual(document.decoded, document.want) {
			t.Fatalf("%s = %#v, want %#v", document.file, document.decoded, document.want)
		}
	}
}

// Decoding catches a renamed tag but not a field leaking into what agents
// read, so the encoded shape of Record is pinned too.
func TestWireShapeRecord(t *testing.T) {
	encoded, err := json.Marshal(Record{Index: 7, Text: "plain text"})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"index":7,"text":"plain text"}` {
		t.Fatalf("record JSON = %s", encoded)
	}
}

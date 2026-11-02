package state

// Every persisted state file is read through readJSON, which decodes
// strictly. A JSON tag renamed without bumping CurrentProtocolVersion lets a
// new binary past the PROTOCOL_MISMATCH gate onto an old root, where it fails
// with a raw decode error instead -- an outage this package has had twice.
//
// Each test below decodes a checked-in fixture of a current on-disk document
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

// decodeWireFixture writes fixture to a temp file and decodes it with the
// real readJSON production path -- the same function every persisted state
// file goes through -- rather than a hand-rolled json.Unmarshal.
func decodeWireFixture[T any](t *testing.T, fixture string) T {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var value T
	if err := readJSON(path, &value); err != nil {
		t.Fatalf("readJSON(%T): %v", value, err)
	}
	return value
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// Agent, written to registry/<name>.json.
const agentFixture = `{
  "name": "agent-a",
  "responsibility": "reviews architecture changes",
  "timestamp": "2026-07-21T12:00:00Z"
}
`

func TestWireCompatAgent(t *testing.T) {
	got := decodeWireFixture[Agent](t, agentFixture)
	want := Agent{
		Name:           "agent-a",
		Responsibility: "reviews architecture changes",
		Timestamp:      mustParseTime(t, "2026-07-21T12:00:00Z"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Agent = %#v, want %#v", got, want)
	}
}

// Publication, written as one line of topics/<group>/<topic>/history.jsonl.
const publicationFixture = `{
  "sequence": 7,
  "topic": "dev/tasks",
  "agent": "agent-a",
  "timestamp": "2026-07-21T12:00:00Z",
  "records": [
    {"index": 1, "text": "hello"}
  ]
}
`

func TestWireCompatPublication(t *testing.T) {
	got := decodeWireFixture[Publication](t, publicationFixture)
	want := Publication{
		Sequence:  7,
		Topic:     "dev/tasks",
		Agent:     "agent-a",
		Timestamp: mustParseTime(t, "2026-07-21T12:00:00Z"),
		Records:   []Record{{Index: 1, Text: "hello"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Publication = %#v, want %#v", got, want)
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

// Cursor, written to cursors/<agent>.json -- the struct at the center of
// the incident this file exists to prevent a repeat of. See the package
// comment above.
const cursorFixture = `{
  "topic_sequences": {"dev/tasks": 9, "dev/tasks#2": 4}
}
`

func TestWireCompatCursor(t *testing.T) {
	got := decodeWireFixture[Cursor](t, cursorFixture)
	want := Cursor{Topics: map[string]int64{"dev/tasks": 9, "dev/tasks#2": 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Cursor = %#v, want %#v", got, want)
	}
}

// Subscription, written to subscriptions/<agent>.json.
const subscriptionFixture = `{
  "topic_groups": ["dev"],
  "topics": ["dev/tasks", "ops/incidents"]
}
`

func TestWireCompatSubscription(t *testing.T) {
	got := decodeWireFixture[Subscription](t, subscriptionFixture)
	want := Subscription{
		TopicGroups: []string{"dev"},
		Topics:      []string{"dev/tasks", "ops/incidents"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Subscription = %#v, want %#v", got, want)
	}
}

// protocolDocument, written to protocol.json -- the compatibility gate
// itself. A rename here would surface as a decode failure on the very
// file meant to produce a clean PROTOCOL_MISMATCH instead.
const protocolDocumentFixture = `{"version": 3}
`

func TestWireCompatProtocolDocument(t *testing.T) {
	got := decodeWireFixture[protocolDocument](t, protocolDocumentFixture)
	version := 3
	want := protocolDocument{Version: &version}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("protocolDocument = %#v, want %#v", got, want)
	}
}

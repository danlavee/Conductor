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
//
// Function-local anonymous structs (state/index.json) cannot be bound from
// here and are not covered.

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

// Publication, written to topics/<group>/<topic>/history/<seq>.json.
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
// read, so the encoded shapes of Record and Summary are pinned too.
func TestWireShapeRecordAndSummary(t *testing.T) {
	for value, want := range map[any]string{
		Record{Index: 7, Text: "plain text"}:                                      `{"index":7,"text":"plain text"}`,
		Summary{Type: "update", Topic: "dev/tasks", Sequence: 4, Agent: "writer"}: `{"type":"update","topic":"dev/tasks","sequence":4,"agent":"writer"}`,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != want {
			t.Fatalf("%T JSON = %s, want %s", value, encoded, want)
		}
	}
}

// Cursor, written to cursors/<agent>.json -- the struct at the center of
// the incident this file exists to prevent a repeat of. See the package
// comment above.
const cursorFixture = `{
  "summary_sequence": 12,
  "summary_ranges": [{"from": 1, "to": 12}],
  "inbox_offset": 4,
  "topic_sequences": {"dev/tasks": 9}
}
`

func TestWireCompatCursor(t *testing.T) {
	got := decodeWireFixture[Cursor](t, cursorFixture)
	want := Cursor{
		Summary:       12,
		SummaryRanges: []IndexRange{{From: 1, To: 12}},
		InboxOffset:   4,
		Topics:        map[string]int64{"dev/tasks": 9},
	}
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

// Event, written to events/<seq>.json.
const eventFixture = `{
  "summary": {
    "type": "update",
    "topic": "dev/tasks",
    "sequence": 12,
    "agent": "agent-a"
  },
  "recipients": ["agent-b", "agent-c"]
}
`

func TestWireCompatEvent(t *testing.T) {
	got := decodeWireFixture[Event](t, eventFixture)
	want := Event{
		Summary: Summary{
			Type:     "update",
			Topic:    "dev/tasks",
			Sequence: 12,
			Agent:    "agent-a",
		},
		Recipients: []string{"agent-b", "agent-c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Event = %#v, want %#v", got, want)
	}
}

// rosterIndexRecord, written to
// state/collaboration-agents-index/<agent>.json.
const rosterIndexRecordFixture = `{"index": 3}
`

func TestWireCompatRosterIndexRecord(t *testing.T) {
	got := decodeWireFixture[rosterIndexRecord](t, rosterIndexRecordFixture)
	want := rosterIndexRecord{Index: 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rosterIndexRecord = %#v, want %#v", got, want)
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

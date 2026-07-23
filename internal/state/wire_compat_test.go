package state

// This file exists because of a real outage, and that outage turned out not
// to be a one-off. Cursor's JSON tags were renamed (signal_index ->
// summary_sequence, resource_indexes -> topic_sequences, signal_ranges ->
// summary_ranges) as part of unrelated feature work, without bumping
// CurrentProtocolVersion. Every persisted state file is read through
// readJSON in storage.go, which uses json.Decoder.DisallowUnknownFields()
// -- strict decoding. Because the protocol version didn't change, deploying
// the new binary against an existing state root skipped the
// PROTOCOL_MISMATCH safety gate entirely and instead failed with a raw,
// confusing decode error the moment any command tried to read an existing
// agent's cursor file.
//
// End-to-end verification of the v2->v3 migration against a real production
// state root (see internal/migrate) then surfaced a second, earlier
// instance of the exact same class of bug, also never caught by a protocol
// bump: Summary and Publication once used resource/index (rather than
// topic/sequence), and Summary carried an extra "key" field with no current
// equivalent. That rename predates this file; it is why Event and
// Publication are covered below alongside Cursor, and it is direct evidence
// that this class of bug recurs whenever a field rename isn't paired with
// this test failing and forcing a conscious decision.
//
// Each test below decodes a small, checked-in "golden" fixture -- a
// realistic, current, correct on-disk JSON document -- through the exact
// same strict-decoding path production code uses (readJSON), for every
// named struct this package persists and reads back that way. If a future
// change renames or removes a JSON tag on one of these structs, decoding
// its fixture here fails immediately (DisallowUnknownFields rejects the
// fixture's now-unrecognized field), forcing a conscious choice at the
// point of that change:
//
//   - keep the on-disk field backward-compatible, or
//   - bump CurrentProtocolVersion, add a migration in internal/migrate,
//     and only then update the fixture to match -- deliberately, in the
//     same change.
//
// A failure here is a meaningful signal, not noise to silence. Do not
// "fix" it by simply editing the fixture to match a renamed field without
// doing one of the two things above.
//
// state/index.json, record-index.json, and head.json are also decoded
// through readJSON's strict path, but as unexported, function-local
// anonymous struct literals with no shared named type. A fixture test
// cannot bind to those types from outside their function, so it provides
// no actual protection for them (renaming their tag in production would
// not make a test here fail); they are intentionally omitted from THIS
// file for that reason, not because they are risk-free -- head.json in
// particular turned out to have the exact same resource/index -> topic/
// sequence drift as Event and Publication in real production data, and
// internal/migrate's RunV2ToV3 translates it accordingly even though no
// fixture test here covers it. protocol.json's protocolDocument, by
// contrast, is a named package-level type and is covered below.

import (
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

// protocol.Agent, written to registry/<name>.json.
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

// protocol.Publication, written to topics/<group>/<topic>/history/<seq>.json.
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

// Lock, written to locks/<encoded>.lock and to reader marker files.
const lockFixture = `{
  "pid": 4242,
  "process_start": "133700000000000000",
  "lease_id": 5,
  "agent": "agent-a",
  "timestamp": "2026-07-21T12:00:00Z",
  "timeout_sec": 180
}
`

func TestWireCompatLock(t *testing.T) {
	got := decodeWireFixture[Lock](t, lockFixture)
	want := Lock{
		PID:          4242,
		ProcessStart: "133700000000000000",
		LeaseID:      5,
		Agent:        "agent-a",
		Timestamp:    mustParseTime(t, "2026-07-21T12:00:00Z"),
		TimeoutSec:   180,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lock = %#v, want %#v", got, want)
	}
}

// Transaction, written to transactions/<agent>.json.
const transactionFixture = `{
  "topic": "dev/tasks",
  "agent": "agent-a",
  "pid": 4242,
  "started": "2026-07-21T12:00:00Z",
  "sequence": 3,
  "records": {"1": {"index": 1, "text": "hello"}},
  "created": {"1": true}
}
`

func TestWireCompatTransaction(t *testing.T) {
	got := decodeWireFixture[Transaction](t, transactionFixture)
	want := Transaction{
		Topic:    "dev/tasks",
		Agent:    "agent-a",
		PID:      4242,
		Started:  mustParseTime(t, "2026-07-21T12:00:00Z"),
		Sequence: 3,
		Records:  map[int64]Record{1: {Index: 1, Text: "hello"}},
		Created:  map[int64]bool{1: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Transaction = %#v, want %#v", got, want)
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

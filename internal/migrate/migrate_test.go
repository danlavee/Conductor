package migrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/state"
	"github.com/danlavee/Conductor/protocol"
)

// writeFile is a small helper: create parent directories and write path
// with the given raw JSON content plus a trailing newline, mirroring how
// the production writeJSON helper formats files.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// legacyInboxLine1 and legacyInboxLine2 are the two v2-shape inbox lines
// used by newV2Fixture, kept as named constants so the fixture's
// inbox_offset can be pinned exactly to a real line boundary (one line
// consumed) instead of an arbitrary byte count, and so
// TestRunV2ToV3TranslatesInboxOffsetAcrossLineLengthChange can compute the
// expected translated offset independently rather than hardcoding it.
const (
	legacyInboxLine1 = `{"type":"join","resource":"registry","key":"agent-a","index":1,"agent":"agent-a"}`
	legacyInboxLine2 = `{"type":"update","resource":"dev/tasks","key":"*","index":2,"agent":"agent-a"}`
)

// newV2Fixture builds a minimal state root matching what a real production
// v2 root was found to actually contain (verified against a copy of the
// live state root): one agent, one committed topic history entry using the
// pre-rename resource/index field names, a topic head.json using index
// instead of sequence, two events (one membership, one content) using the
// pre-rename signal/resource/key/index shape, a two-line inbox file in the
// same pre-rename shape with its cursor's inbox_offset pinned exactly to
// the end of line 1 (one delivery already consumed), and a cursor file in
// the pre-rename signal_index/signal_ranges/resource_indexes shape. Both
// the cursor rename and the resource/index -> topic/sequence rename shipped
// inside v2's lifetime without ever bumping CurrentProtocolVersion.
func newV2Fixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "v2-source")
	writeFile(t, filepath.Join(root, "protocol.json"), `{"version": 2}`)
	writeFile(t, filepath.Join(root, "registry", "agent-a.json"), `{
  "name": "agent-a",
  "responsibility": "dev",
  "timestamp": "2026-01-01T00:00:00Z"
}`)
	writeFile(t, filepath.Join(root, "subscriptions", "agent-a.json"), `{"topic_groups":[],"topics":[]}`)
	writeFile(t, filepath.Join(root, "state", "index.json"), `{"index": 2}`)
	writeFile(t, filepath.Join(root, "topics", "dev", "tasks", "head.json"), `{"index": 1}`)
	writeFile(t, filepath.Join(root, "topics", "dev", "tasks", "record-index.json"), `{"index": 1}`)
	writeFile(t, filepath.Join(root, "topics", "dev", "tasks", "history", "00000000000000000001.json"), `{
  "index": 1,
  "resource": "dev/tasks",
  "agent": "agent-a",
  "timestamp": "2026-01-01T00:00:00Z",
  "records": [{"index": 1, "text": "hello"}]
}`)
	legacyInboxOffset := len(legacyInboxLine1) + 1 // one full line ("\n" included) already consumed
	writeFile(t, filepath.Join(root, "cursors", "agent-a.json"), fmt.Sprintf(`{
  "signal_index": 5,
  "signal_ranges": [{"from": 1, "to": 5}],
  "inbox_offset": %d,
  "resource_indexes": {"dev/tasks": 3}
}`, legacyInboxOffset))
	writeFile(t, filepath.Join(root, "events", "00000000000000000001.json"), `{
  "signal": {
    "type": "join",
    "resource": "registry",
    "key": "agent-a",
    "index": 1,
    "agent": "agent-a"
  },
  "recipients": ["agent-a"]
}`)
	writeFile(t, filepath.Join(root, "events", "00000000000000000002.json"), `{
  "signal": {
    "type": "update",
    "resource": "dev/tasks",
    "key": "*",
    "index": 2,
    "agent": "agent-a"
  },
  "recipients": ["agent-a"]
}`)
	// inbox/<agent> is a newline-delimited (JSONL) log of compact Summary
	// values, not a single JSON document; a real production inbox file with
	// an unread old-shape line was found during end-to-end verification
	// (offset 0, file nonempty) -- exactly the same failure mode as events
	// and history, just easy to miss since most agents happen to be fully
	// caught up (offset == file size) at any given snapshot.
	writeFile(t, filepath.Join(root, "inbox", "agent-a"), legacyInboxLine1+"\n"+legacyInboxLine2+"\n")
	return root
}

func TestRunV2ToV3TranslatesLegacyShapesAndCopiesEverythingElse(t *testing.T) {
	source := newV2Fixture(t)
	destination := filepath.Join(t.TempDir(), "v3-destination")

	report, err := RunV2ToV3(source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if report.FromVersion != 2 || report.ToVersion != 3 {
		t.Fatalf("report = %#v", report)
	}
	if report.CursorsMigrated != 1 || report.EventsMigrated != 2 || report.PublicationsMigrated != 1 || report.TopicHeadsMigrated != 1 || report.InboxLinesMigrated != 2 {
		t.Fatalf("report translation counts = %#v", report)
	}
	if report.Source != source || report.Destination != destination {
		t.Fatalf("report source/destination = %q, %q", report.Source, report.Destination)
	}

	var protocolDoc struct {
		Version int `json:"version"`
	}
	if data, err := os.ReadFile(filepath.Join(destination, "protocol.json")); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(data, &protocolDoc); err != nil || protocolDoc.Version != 3 {
		t.Fatalf("destination protocol.json = %s", data)
	}

	// The translated inbox_offset must land at the end of the translated
	// line 1, not at the old line 1's (longer, "key"-carrying) length --
	// otherwise it would point at the wrong byte in the rewritten file, a
	// silent replay-or-skip bug distinct from the decode errors this
	// migration otherwise guards against. Computed independently here
	// (not hardcoded) so this assertion breaks if either encoding changes.
	wantTranslatedLine1, err := json.Marshal(state.Summary{Type: "join", Topic: "registry", Sequence: 1, Agent: "agent-a"})
	if err != nil {
		t.Fatal(err)
	}
	wantInboxOffset := int64(len(wantTranslatedLine1) + 1)
	if wantInboxOffset == int64(len(legacyInboxLine1)+1) {
		t.Fatal("test fixture bug: translated and legacy line 1 lengths coincide, offset-remap assertion would not be meaningful")
	}

	var cursor state.Cursor
	cursorData, err := os.ReadFile(filepath.Join(destination, "cursors", "agent-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(cursorData, &cursor); err != nil {
		t.Fatal(err)
	}
	wantCursor := state.Cursor{
		Summary:       5,
		SummaryRanges: []state.IndexRange{{From: 1, To: 5}},
		InboxOffset:   wantInboxOffset,
		Topics:        map[string]int64{"dev/tasks": 3},
	}
	if !reflect.DeepEqual(cursor, wantCursor) {
		t.Fatalf("cursor = %#v, want %#v", cursor, wantCursor)
	}
	if strings.Contains(string(cursorData), "signal_index") || strings.Contains(string(cursorData), "resource_indexes") {
		t.Fatalf("destination cursor retains v2 field names: %s", cursorData)
	}

	var head struct {
		Sequence int64 `json:"sequence"`
	}
	headData, err := os.ReadFile(filepath.Join(destination, "topics", "dev", "tasks", "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(headData, &head); err != nil || head.Sequence != 1 {
		t.Fatalf("destination head.json = %s", headData)
	}

	var publication protocol.Publication
	publicationData, err := os.ReadFile(filepath.Join(destination, "topics", "dev", "tasks", "history", "00000000000000000001.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(publicationData, &publication); err != nil {
		t.Fatal(err)
	}
	wantPublication := protocol.Publication{
		Sequence:  1,
		Topic:     "dev/tasks",
		Agent:     "agent-a",
		Timestamp: mustParseTime(t, "2026-01-01T00:00:00Z"),
		Records:   []protocol.Record{{Index: 1, Text: "hello"}},
	}
	if !reflect.DeepEqual(publication, wantPublication) {
		t.Fatalf("publication = %#v, want %#v", publication, wantPublication)
	}
	if strings.Contains(string(publicationData), `"resource"`) {
		t.Fatalf("destination publication retains v2 field names: %s", publicationData)
	}

	var joinEvent, updateEvent state.Event
	joinData, err := os.ReadFile(filepath.Join(destination, "events", "00000000000000000001.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(joinData, &joinEvent); err != nil {
		t.Fatal(err)
	}
	wantJoin := state.Event{
		Summary:    state.Summary{Type: "join", Topic: "registry", Sequence: 1, Agent: "agent-a"},
		Recipients: []string{"agent-a"},
	}
	if !reflect.DeepEqual(joinEvent, wantJoin) {
		t.Fatalf("join event = %#v, want %#v", joinEvent, wantJoin)
	}
	if strings.Contains(string(joinData), `"key"`) {
		t.Fatalf("destination join event retains dropped v2 key field: %s", joinData)
	}

	updateData, err := os.ReadFile(filepath.Join(destination, "events", "00000000000000000002.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(updateData, &updateEvent); err != nil {
		t.Fatal(err)
	}
	wantUpdate := state.Event{
		Summary:    state.Summary{Type: "update", Topic: "dev/tasks", Sequence: 2, Agent: "agent-a"},
		Recipients: []string{"agent-a"},
	}
	if !reflect.DeepEqual(updateEvent, wantUpdate) {
		t.Fatalf("update event = %#v, want %#v", updateEvent, wantUpdate)
	}

	inboxData, err := os.ReadFile(filepath.Join(destination, "inbox", "agent-a"))
	if err != nil {
		t.Fatal(err)
	}
	inboxLines := strings.Split(strings.TrimRight(string(inboxData), "\n"), "\n")
	if len(inboxLines) != 2 {
		t.Fatalf("inbox lines = %d, want 2: %s", len(inboxLines), inboxData)
	}
	var inboxJoin, inboxUpdate state.Summary
	if err := json.Unmarshal([]byte(inboxLines[0]), &inboxJoin); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(inboxLines[1]), &inboxUpdate); err != nil {
		t.Fatal(err)
	}
	if want := (state.Summary{Type: "join", Topic: "registry", Sequence: 1, Agent: "agent-a"}); inboxJoin != want {
		t.Fatalf("inbox join line = %#v, want %#v", inboxJoin, want)
	}
	if want := (state.Summary{Type: "update", Topic: "dev/tasks", Sequence: 2, Agent: "agent-a"}); inboxUpdate != want {
		t.Fatalf("inbox update line = %#v, want %#v", inboxUpdate, want)
	}
	if strings.Contains(string(inboxData), `"key"`) || strings.Contains(string(inboxData), `"resource"`) {
		t.Fatalf("destination inbox retains v2 field names: %s", inboxData)
	}

	for _, relative := range []string{
		filepath.Join("registry", "agent-a.json"),
		filepath.Join("subscriptions", "agent-a.json"),
		filepath.Join("topics", "dev", "tasks", "record-index.json"),
		filepath.Join("state", "index.json"),
	} {
		before, err := os.ReadFile(filepath.Join(source, relative))
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(filepath.Join(destination, relative))
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatalf("%s changed during migration:\nbefore: %s\nafter:  %s", relative, before, after)
		}
	}
}

func TestRunV2ToV3RejectsWrongSourceProtocol(t *testing.T) {
	source := newV2Fixture(t)
	writeFile(t, filepath.Join(source, "protocol.json"), `{"version": 1}`)
	destination := filepath.Join(t.TempDir(), "v3-destination")
	if _, err := RunV2ToV3(source, destination); err == nil {
		t.Fatal("expected an error for a non-v2 source")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination created despite rejected source: %v", err)
	}
}

func TestRunV2ToV3RejectsNonEmptyDestination(t *testing.T) {
	source := newV2Fixture(t)
	destination := t.TempDir()
	writeFile(t, filepath.Join(destination, "existing.txt"), "keep")
	if _, err := RunV2ToV3(source, destination); err == nil {
		t.Fatal("expected an error for a non-empty destination")
	}
	if data, err := os.ReadFile(filepath.Join(destination, "existing.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("destination contents changed: %q, %v", data, err)
	}
}

func TestRunV2ToV3RejectsUncommittedTransactions(t *testing.T) {
	source := newV2Fixture(t)
	writeFile(t, filepath.Join(source, "transactions", "agent-a.json"), `{
  "topic": "dev/tasks",
  "agent": "agent-a",
  "pid": 1,
  "started": "2026-01-01T00:00:00Z",
  "records": {},
  "created": {}
}`)
	destination := filepath.Join(t.TempDir(), "v3-destination")
	if _, err := RunV2ToV3(source, destination); err == nil {
		t.Fatal("expected an error for an uncommitted transaction")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination created despite an active transaction: %v", err)
	}
}

func TestRunV2ToV3RejectsRelativePaths(t *testing.T) {
	if _, err := RunV2ToV3("relative-source", "relative-destination"); err == nil {
		t.Fatal("expected an error for relative paths")
	}
}

func TestRunV2ToV3RejectsSameSourceAndDestination(t *testing.T) {
	source := newV2Fixture(t)
	if _, err := RunV2ToV3(source, source); err == nil {
		t.Fatal("expected an error when destination equals source")
	}
}

func TestDetectSourceVersion(t *testing.T) {
	source := newV2Fixture(t)
	version, err := DetectSourceVersion(source)
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("version = %d, want 2", version)
	}
}

package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
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

// writeFile creates parent directories and writes the supplied fixture exactly.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
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
	writeFile(t, filepath.Join(root, "topics-backup", "dev", "tasks", "head.json"), `{"keep":"head"}`)
	writeFile(t, filepath.Join(root, "archive", "history", "item.json"), `{"keep":"history"}`)
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
	dottedInboxLine := `{"type":"update","resource":"dev/tasks","key":"*","index":7,"agent":"agent.a"}`
	writeFile(t, filepath.Join(root, "inbox", "agent.a"), dottedInboxLine+"\n")
	writeFile(t, filepath.Join(root, "cursors", "agent.a.json"), fmt.Sprintf(`{
  "signal_index": 1,
  "signal_ranges": [{"from": 1, "to": 1}],
  "resource_indexes": {"dev/tasks": 1},
  "summary_sequence": 9,
  "summary_ranges": [{"from": 2, "to": 9}],
  "inbox_offset": %d,
  "topic_sequences": {"dev/tasks": 7}
}`, len(dottedInboxLine)+1))
	return root
}

func TestRunDispatchesV1Migration(t *testing.T) {
	source := filepath.Join(t.TempDir(), "v1-source")
	writeFile(t, filepath.Join(source, "protocol.json"), `{"version": 1}`)
	writeFile(t, filepath.Join(source, "registry", "agent-a.json"), `{
  "name": "agent-a",
  "responsibility": "dev",
  "timestamp": "2026-01-01T00:00:00Z"
}`)
	writeFile(t, filepath.Join(source, "topics", "dev", "tasks", "history", "00000000000000000001.json"), `{
  "index": 1,
  "resource": "dev/tasks",
  "agent": "agent-a",
  "timestamp": "2026-01-01T00:00:00Z",
  "messages": {
    "task": {
      "operation": "set",
      "payload": {"text": "hello"}
    }
  }
}`)
	destination := filepath.Join(t.TempDir(), "destination")
	sourceBefore := snapshotFiles(t, source)

	report, err := Run(source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if report.FromVersion != 1 || report.Records != 1 || report.Topics != 1 {
		t.Fatalf("report = %+v", report)
	}
	if !reflect.DeepEqual(snapshotFiles(t, source), sourceBefore) {
		t.Fatal("source changed during migration")
	}
	if _, err := os.Stat(filepath.Join(destination, "topics", "dev", "tasks", "history.jsonl")); err != nil {
		t.Fatalf("migrated history: %v", err)
	}
	client, err := state.New(destination, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Get(state.ReadRequest{Topic: "dev/tasks", Mode: state.ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 1 || result.Records[0].Text != "hello" {
		t.Fatalf("migrated records = %#v", result.Records)
	}
}

func TestRunV2ToV3TranslatesLegacyShapesAndCopiesEverythingElse(t *testing.T) {
	source := newV2Fixture(t)
	destination := filepath.Join(t.TempDir(), "v3-destination")
	sourceBefore := snapshotFiles(t, source)

	report, err := Run(source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if report.FromVersion != 2 || report.ToVersion != 3 {
		t.Fatalf("report = %#v", report)
	}
	if report.CursorsMigrated != 2 || report.EventsMigrated != 2 || report.PublicationsMigrated != 1 || report.TopicHeadsMigrated != 1 || report.InboxLinesMigrated != 3 {
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
	dottedCursorData, err := os.ReadFile(filepath.Join(destination, "cursors", "agent.a.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dottedCursor state.Cursor
	if err := json.Unmarshal(dottedCursorData, &dottedCursor); err != nil {
		t.Fatal(err)
	}
	wantDottedCursor := state.Cursor{
		Summary:       9,
		SummaryRanges: []state.IndexRange{{From: 2, To: 9}},
		InboxOffset:   int64(len(`{"type":"update","topic":"dev/tasks","sequence":7,"agent":"agent.a"}`) + 1),
		Topics:        map[string]int64{"dev/tasks": 7},
	}
	if !reflect.DeepEqual(dottedCursor, wantDottedCursor) {
		t.Fatalf("dotted cursor = %#v, want %#v", dottedCursor, wantDottedCursor)
	}
	dottedInbox, err := os.ReadFile(filepath.Join(destination, "inbox", "agent.a"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dottedInbox), `"resource"`) || strings.Contains(string(dottedInbox), `"key"`) {
		t.Fatalf("dotted-agent inbox was overwritten with legacy content: %s", dottedInbox)
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
		filepath.Join("topics-backup", "dev", "tasks", "head.json"),
		filepath.Join("archive", "history", "item.json"),
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
	if !reflect.DeepEqual(snapshotFiles(t, source), sourceBefore) {
		t.Fatal("source changed during migration")
	}
}

func TestRunV2ToV3RejectsWrongSourceProtocol(t *testing.T) {
	source := newV2Fixture(t)
	writeFile(t, filepath.Join(source, "protocol.json"), `{"version": 1}`)
	destination := filepath.Join(t.TempDir(), "v3-destination")
	if _, err := runV2ToV3(source, destination); err == nil {
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
	if _, err := runV2ToV3(source, destination); err == nil {
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
	if _, err := runV2ToV3(source, destination); err == nil {
		t.Fatal("expected an error for an uncommitted transaction")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination created despite an active transaction: %v", err)
	}
}

func TestRunV2ToV3RejectsRelativePaths(t *testing.T) {
	if _, err := runV2ToV3("relative-source", "relative-destination"); err == nil {
		t.Fatal("expected an error for relative paths")
	}
}

func TestRunV2ToV3RejectsSameSourceAndDestination(t *testing.T) {
	source := newV2Fixture(t)
	if _, err := runV2ToV3(source, source); err == nil {
		t.Fatal("expected an error when destination equals source")
	}
}

func TestPrepareMigrationValidationLeavesDestinationUntouched(t *testing.T) {
	t.Run("relative paths", func(t *testing.T) {
		preparation, err := prepareMigration("relative-source", "relative-destination", 3, 4)
		if err == nil || err.Error() != "migration source and destination must be absolute paths" {
			t.Fatalf("error = %v", err)
		}
		if preparation.report.FromVersion != 3 || preparation.report.ToVersion != 4 {
			t.Fatalf("report = %+v", preparation.report)
		}
	})

	t.Run("identical paths", func(t *testing.T) {
		source := t.TempDir()
		preparation, err := prepareMigration(source, source, 3, 4)
		if err == nil || err.Error() != "migration destination must differ from source" {
			t.Fatalf("error = %v", err)
		}
		if preparation.report.Source != source || preparation.report.Destination != source {
			t.Fatalf("report = %+v", preparation.report)
		}
	})

	t.Run("active transaction", func(t *testing.T) {
		parent := t.TempDir()
		source := filepath.Join(parent, "source")
		destination := filepath.Join(parent, "destination")
		writeFile(t, filepath.Join(source, "protocol.json"), `{"version": 3}`)
		writeFile(t, filepath.Join(source, "transactions", "agent-a.json"), `{}`)

		if _, err := prepareMigration(source, destination, 3, 4); err == nil {
			t.Fatal("active transaction was accepted")
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("destination changed after transaction rejection: %v", err)
		}
	})

	t.Run("non-empty destination", func(t *testing.T) {
		parent := t.TempDir()
		source := filepath.Join(parent, "source")
		destination := filepath.Join(parent, "destination")
		writeFile(t, filepath.Join(source, "protocol.json"), `{"version": 3}`)
		writeFile(t, filepath.Join(destination, "existing.txt"), "keep")

		if _, err := prepareMigration(source, destination, 3, 4); err == nil {
			t.Fatal("non-empty destination was accepted")
		}
		if data, err := os.ReadFile(filepath.Join(destination, "existing.txt")); err != nil || string(data) != "keep" {
			t.Fatalf("destination content = %q, %v", data, err)
		}
	})
}

func TestPrepareMigrationCreatesStage(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	destination := filepath.Join(parent, "destination")
	writeFile(t, filepath.Join(source, "protocol.json"), `{"version": 3}`)

	preparation, err := prepareMigration(source, destination, 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(preparation.stage) })
	if preparation.source != source || preparation.destination != destination || preparation.destinationExists {
		t.Fatalf("preparation = %+v", preparation)
	}
	if info, err := os.Stat(preparation.stage); err != nil || !info.IsDir() {
		t.Fatalf("stage was not created: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination was created during preparation: %v", err)
	}
}

func TestDetectSourceVersion(t *testing.T) {
	source := newV2Fixture(t)
	version, err := detectSourceVersion(source)
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("version = %d, want 2", version)
	}
}

func TestRunV3ToV4Migration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "v3-source")
	writeFile(t, filepath.Join(root, "protocol.json"), `{"version": 3}`)
	writeFile(t, filepath.Join(root, "registry", "agent-a.json"), `{
  "name": "agent-a",
  "responsibility": "dev",
  "timestamp": "2026-01-01T00:00:00Z"
}`)
	writeFile(t, filepath.Join(root, "topics", "dev", "tasks", "head.json"), `{"sequence": 2}`)
	writeFile(t, filepath.Join(root, "topics", "dev", "tasks", "record-index.json"), `{"index": 2}`)
	writeFile(t, filepath.Join(root, "topics", "dev", "tasks", "history", "00000000000000000001.json"), `{
  "sequence": 1,
  "topic": "dev/tasks",
  "agent": "agent-a",
  "timestamp": "2026-01-01T00:00:00Z",
  "records": [{"index": 1, "text": "hello"}]
}`)
	writeFile(t, filepath.Join(root, "topics", "dev", "tasks", "history", "00000000000000000002.json"), `{
  "sequence": 2,
  "topic": "dev/tasks",
  "agent": "agent-a",
  "timestamp": "2026-01-02T00:00:00Z",
  "records": [{"index": 2, "text": "world"}]
}`)
	writeFile(t, filepath.Join(root, "topics-backup", "keep.txt"), "keep")
	writeFile(t, filepath.Join(root, "topics", "dev", "tasks", "raw-only.txt"), "do not copy")

	destination := filepath.Join(t.TempDir(), "v4-destination")
	report, err := Run(root, destination)
	if err != nil {
		t.Fatal(err)
	}

	if report.Topics != 1 || report.Records != 2 {
		t.Fatalf("report = %+v", report)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "topics-backup", "keep.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("unrelated topics-prefixed path = %q, %v", data, err)
	}

	// Verify protocol.json is v4
	protocolData, err := os.ReadFile(filepath.Join(destination, "protocol.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(protocolData), `"version": 4`) {
		t.Fatalf("unexpected protocol version: %s", protocolData)
	}

	// Verify history.jsonl is populated
	jsonlData, err := os.ReadFile(filepath.Join(destination, "topics", "dev", "tasks", "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(jsonlData)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines in history.jsonl, got %d:\n%s", len(lines), jsonlData)
	}

	firstLine, err := json.Marshal(protocol.Publication{
		Sequence:  1,
		Topic:     "dev/tasks",
		Agent:     "agent-a",
		Timestamp: mustParseTime(t, "2026-01-01T00:00:00Z"),
		Records:   []protocol.Record{{Index: 1, Text: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondLine, err := json.Marshal(protocol.Publication{
		Sequence:  2,
		Topic:     "dev/tasks",
		Agent:     "agent-a",
		Timestamp: mustParseTime(t, "2026-01-02T00:00:00Z"),
		Records:   []protocol.Record{{Index: 2, Text: "world"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantJSONL := append(append(append(firstLine, '\n'), secondLine...), '\n')
	if !bytes.Equal(jsonlData, wantJSONL) {
		t.Fatalf("history.jsonl = %s, want %s", jsonlData, wantJSONL)
	}

	var pub1, pub2 protocol.Publication
	if err := json.Unmarshal([]byte(lines[0]), &pub1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &pub2); err != nil {
		t.Fatal(err)
	}

	if pub1.Sequence != 1 || pub1.Records[0].Text != "hello" {
		t.Fatalf("pub1 = %+v", pub1)
	}
	if pub2.Sequence != 2 || pub2.Records[0].Text != "world" {
		t.Fatalf("pub2 = %+v", pub2)
	}

	// Verify old structures do NOT exist
	if _, err := os.Stat(filepath.Join(destination, "topics", "dev", "tasks", "head.json")); !os.IsNotExist(err) {
		t.Fatal("head.json should not exist in v4")
	}
	if _, err := os.Stat(filepath.Join(destination, "topics", "dev", "tasks", "history")); !os.IsNotExist(err) {
		t.Fatal("history/ directory should not exist in v4")
	}
	if _, err := os.Stat(filepath.Join(destination, "topics", "dev", "tasks", "raw-only.txt")); !os.IsNotExist(err) {
		t.Fatal("unrecognized raw topic state should not be copied into v4")
	}
}

func TestRunRejectsUnsupportedSourceVersion(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "protocol.json"), `{"version": 4}`)

	_, err := Run(source, filepath.Join(t.TempDir(), "destination"))
	if err == nil || err.Error() != "migrate supports v1, v2 or v3 source roots, found protocol 4" {
		t.Fatalf("error = %v", err)
	}
}

func TestRunV3ToV4CleansStageAfterHistoryFailure(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "v3-source")
	writeFile(t, filepath.Join(source, "protocol.json"), `{"version": 3}`)
	writeFile(t, filepath.Join(source, "topics", "dev", "tasks", "history", "00000000000000000001.json"), `{broken`)
	sourceBefore := snapshotFiles(t, source)
	destination := filepath.Join(parent, "destination")

	if _, err := Run(source, destination); err == nil {
		t.Fatal("expected malformed history to fail migration")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failed migration: %v", err)
	}
	stages, err := filepath.Glob(filepath.Join(parent, ".conductor-migrate-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 0 {
		t.Fatalf("staging directories remain after failure: %v", stages)
	}
	if !reflect.DeepEqual(snapshotFiles(t, source), sourceBefore) {
		t.Fatal("source changed during failed migration")
	}
}

func TestTranslateInboxPreservesUnterminatedTailAndRecoveryOffsets(t *testing.T) {
	tail := `{"type":"update","resource":"dev/tasks","index":99,"agent":"agent-a"}`
	source := []byte(legacyInboxLine1 + "\n\n" + tail)

	translated, lengths, err := translateInboxContent(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(lengths) != 2 || lengths[1] != (inboxLineLength{sourceBytes: 1, translatedBytes: 1}) {
		t.Fatalf("translated complete lines = %#v", lengths)
	}
	if !bytes.HasSuffix(translated, []byte(tail)) || translated[len(translated)-1] == '\n' {
		t.Fatalf("unterminated tail was changed or completed: %q", translated)
	}
	if offset := translateInboxOffset(lengths, int64(len(source)+1), int64(len(source))); offset != 0 {
		t.Fatalf("oversized offset = %d, want replay offset 0", offset)
	}
	oldCompleteSize := int64(len(legacyInboxLine1) + 1)
	if offset := translateInboxOffset(lengths, oldCompleteSize, int64(len(source))); offset != lengths[0].translatedBytes {
		t.Fatalf("complete-line offset = %d, want %d", offset, lengths[0].translatedBytes)
	}
	if offset := translateInboxOffset(lengths, oldCompleteSize+1, int64(len(source))); offset != lengths[0].translatedBytes+1 {
		t.Fatalf("empty-line offset = %d, want %d", offset, lengths[0].translatedBytes+1)
	}
}

func TestPublishStageRestoresExistingEmptyDestinationAfterRenameFailure(t *testing.T) {
	parent := t.TempDir()
	stage := filepath.Join(parent, "stage")
	destination := filepath.Join(parent, "destination")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	renameFailure := errors.New("rename failed")

	err = publishStageWith(
		stage,
		destination,
		true,
		func(source, target string) error {
			if source == stage {
				return renameFailure
			}
			return os.Rename(source, target)
		},
		os.RemoveAll,
	)
	if !errors.Is(err, renameFailure) {
		t.Fatalf("error = %v, want rename failure", err)
	}
	info, err := os.Stat(destination)
	if err != nil || !info.IsDir() {
		t.Fatalf("empty destination was not restored: %v", err)
	}
	if !os.SameFile(original, info) {
		t.Fatal("publication rollback replaced the original destination object")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("restored destination is not empty: %v", entries)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("failed publication removed staging before caller cleanup: %v", err)
	}
}

func TestPublishStageCleansBackupAfterDestinationAsideFailure(t *testing.T) {
	parent := t.TempDir()
	stage := filepath.Join(parent, "stage")
	destination := filepath.Join(parent, "destination")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	renameFailure := errors.New("aside failed")
	cleanupCalled := false

	err = publishStageWith(
		stage,
		destination,
		true,
		func(source, target string) error {
			if source == destination {
				return renameFailure
			}
			return os.Rename(source, target)
		},
		func(path string) error {
			cleanupCalled = true
			return os.RemoveAll(path)
		},
	)
	if !errors.Is(err, renameFailure) {
		t.Fatalf("error = %v, want aside failure", err)
	}
	if !cleanupCalled {
		t.Fatal("backup root cleanup was not attempted")
	}
	current, err := os.Stat(destination)
	if err != nil || !os.SameFile(original, current) {
		t.Fatalf("destination changed after aside failure: %v", err)
	}
	backups, err := filepath.Glob(filepath.Join(parent, ".conductor-destination-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("destination backups remain: %v", backups)
	}
}

func TestPublishStageDistinguishesCommittedBackupCleanupFailure(t *testing.T) {
	parent := t.TempDir()
	stage := filepath.Join(parent, "stage")
	destination := filepath.Join(parent, "destination")
	writeFile(t, filepath.Join(stage, "committed.txt"), "committed")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("cleanup failed")

	err := publishStageWith(stage, destination, true, os.Rename, func(string) error {
		return cleanupFailure
	})
	var committed *committedMigrationError
	if !errors.As(err, &committed) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("error = %v, want committed cleanup failure", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(destination, "committed.txt")); readErr != nil || string(data) != "committed" {
		t.Fatalf("published destination = %q, %v", data, readErr)
	}
}

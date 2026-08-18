package migrate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/danlavee/Conductor/internal/state"
	"github.com/danlavee/Conductor/protocol"
)

func newV1SemanticFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "v1-source")
	writeFile(t, filepath.Join(root, "protocol.json"), `{"version":1}`)
	writeFile(t, filepath.Join(root, "state", "index.json"), `{"index":12}`)
	writeFile(t, filepath.Join(root, "registry", "agent-a.json"), `{
  "name":"agent-a",
  "responsibility":"development",
  "timestamp":"2026-01-01T00:00:00Z"
}`)
	writeFile(t, filepath.Join(root, "registry", "agent-b.json"), `{
  "name":"agent-b",
  "responsibility":"operations",
  "timestamp":"2026-01-01T00:00:00Z"
}`)
	writeFile(t, filepath.Join(root, "topics", "dev", "tasks", "history", "00000000000000000001.json"), `{
  "index":1,
  "resource":"dev/tasks",
  "agent":"agent-a",
  "timestamp":"2026-01-01T00:01:00Z",
  "messages":{
    "zeta":{"operation":"set","payload":{"text":"z"}},
    "ghost":{"operation":"scratch"},
    "alpha":{"operation":"set","kind":"decision","payload":{"text":"one"}}
  }
}`)
	writeFile(t, filepath.Join(root, "topics", "dev", "tasks", "history", "00000000000000000002.json"), `{
  "index":2,
  "resource":"dev/tasks",
  "agent":"agent-b",
  "timestamp":"2026-01-01T00:02:00Z",
  "messages":{
    "zeta":{"operation":"scratch"},
    "alpha":{"operation":"set","payload":{"text":"two"}}
  }
}`)
	writeFile(t, filepath.Join(root, "topics", "ops", "notes", "history", "00000000000000000004.json"), `{
  "index":4,
  "resource":"ops/notes",
  "agent":"agent-b",
  "timestamp":"2026-01-01T00:04:00Z",
  "messages":{
    "note-b":{"operation":"set","payload":{"text":"B"}},
    "note-a":{"operation":"set","payload":{"text":"A"}}
  }
}`)
	return root
}

func TestRunV1ToV4PublishesOnlySemanticallyValidatedCurrentStorage(t *testing.T) {
	source := newV1SemanticFixture(t)
	sourceBefore := snapshotFiles(t, source)
	destination := filepath.Join(t.TempDir(), "v4-destination")

	report, err := Run(source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if report.FromVersion != 1 || report.ToVersion != protocolVersion4 ||
		report.Agents != 2 || report.Topics != 2 || report.Records != 4 ||
		report.DiscardedKinds != 1 || report.SkippedScratch != 1 {
		t.Fatalf("report = %+v", report)
	}
	wantMappings := []Mapping{
		{Topic: "dev/tasks", Key: "alpha", Index: 1},
		{Topic: "dev/tasks", Key: "zeta", Index: 2},
		{Topic: "ops/notes", Key: "note-a", Index: 1},
		{Topic: "ops/notes", Key: "note-b", Index: 2},
	}
	if !reflect.DeepEqual(report.Mappings, wantMappings) {
		t.Fatalf("mappings = %#v, want %#v", report.Mappings, wantMappings)
	}
	if !reflect.DeepEqual(snapshotFiles(t, source), sourceBefore) {
		t.Fatal("source changed during migration")
	}

	for _, topic := range []string{"dev/tasks", "ops/notes"} {
		topicDir, err := topicPath(destination, topic)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(topicDir, "history.jsonl")); err != nil {
			t.Fatalf("%s canonical history: %v", topic, err)
		}
		for _, obsolete := range []string{"history", "head.json"} {
			if _, err := os.Stat(filepath.Join(topicDir, obsolete)); !os.IsNotExist(err) {
				t.Fatalf("%s should not contain obsolete %s", topic, obsolete)
			}
		}
	}

	client, err := state.New(destination, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	for topic, want := range map[string][]protocol.Record{
		"dev/tasks": {{Index: 1, Text: "two"}, {Index: 2, Text: "~~z~~"}},
		"ops/notes": {{Index: 1, Text: "A"}, {Index: 2, Text: "B"}},
	} {
		result, err := client.Get(state.ReadRequest{Topic: topic, Mode: state.ReadFull})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.Records, want) || result.Remaining != 0 {
			t.Fatalf("%s full read = %#v remaining %d, want %#v", topic, result.Records, result.Remaining, want)
		}
	}
	subscription, err := client.Subscription()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(subscription.Topics, []string{"dev/tasks", "ops/notes"}) {
		t.Fatalf("subscription topics = %#v", subscription.Topics)
	}
	var sequence struct {
		Index int64 `json:"index"`
	}
	if err := readJSON(filepath.Join(destination, "state", "index.json"), &sequence); err != nil {
		t.Fatal(err)
	}
	if sequence.Index != 12 {
		t.Fatalf("global sequence = %d, want 12", sequence.Index)
	}
	var storedReport Report
	if err := readJSON(filepath.Join(destination, "migration-report.json"), &storedReport); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedReport, report) {
		t.Fatalf("stored report = %#v, returned report %#v", storedReport, report)
	}
}

func TestRunV1ToV4HandlesEmptyAndNoAgentSources(t *testing.T) {
	t.Run("empty root", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "v1-source")
		writeFile(t, filepath.Join(source, "protocol.json"), `{"version":1}`)
		if err := os.MkdirAll(filepath.Join(source, "registry"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(source, "topics"), 0o700); err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(t.TempDir(), "v4-destination")

		report, err := Run(source, destination)
		if err != nil {
			t.Fatal(err)
		}
		if report.Agents != 0 || report.Topics != 0 || report.Records != 0 {
			t.Fatalf("report = %+v", report)
		}
		client, err := state.New(destination, "")
		if err != nil {
			t.Fatal(err)
		}
		groups, err := client.ListTopicGroups()
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != 0 {
			t.Fatalf("topic groups = %#v", groups)
		}
	})

	t.Run("topic without registered agent", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "v1-source")
		writeFile(t, filepath.Join(source, "protocol.json"), `{"version":1}`)
		if err := os.MkdirAll(filepath.Join(source, "registry"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(source, "topics", "dev", "tasks", "history", "00000000000000000001.json"), `{
  "index":1,
  "resource":"dev/tasks",
  "agent":"departed-agent",
  "timestamp":"2026-01-01T00:00:00Z",
  "messages":{"task":{"operation":"set","payload":{"text":"retained"}}}
}`)
		destination := filepath.Join(t.TempDir(), "v4-destination")

		report, err := Run(source, destination)
		if err != nil {
			t.Fatal(err)
		}
		if report.Agents != 0 || report.Topics != 1 || report.Records != 1 {
			t.Fatalf("report = %+v", report)
		}
		entries, err := os.ReadDir(filepath.Join(destination, "registry"))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("migration persisted validation registry entries: %#v", entries)
		}
		client, err := state.New(destination, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Join("reader", "validation"); err != nil {
			t.Fatal(err)
		}
		result, err := client.Get(state.ReadRequest{Topic: "dev/tasks", Mode: state.ReadFull})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.Records, []protocol.Record{{Index: 1, Text: "retained"}}) {
			t.Fatalf("migrated records = %#v", result.Records)
		}
	})
}

func TestValidateV1StageRejectsUnreadableCurrentStorage(t *testing.T) {
	migration := v1TopicMigration{
		topics:         []string{"dev/tasks"},
		mappings:       []Mapping{{Topic: "dev/tasks", Key: "task", Index: 1}},
		currentRecords: map[string]map[int64]string{"dev/tasks": {1: "hello"}},
		records:        1,
	}
	report := Report{
		FromVersion: 1,
		ToVersion:   protocolVersion4,
		Agents:      1,
		Topics:      1,
		Records:     1,
		Mappings:    append([]Mapping(nil), migration.mappings...),
	}
	agent := protocol.Agent{Name: "agent-a", Responsibility: "development"}

	t.Run("released per-publication layout", func(t *testing.T) {
		stage := filepath.Join(t.TempDir(), "stage")
		if err := initializeV4Root(stage); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(stage, "registry", "agent-a.json"), agent); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(stage, "topics", "dev", "tasks", "history", "00000000000000000001.json"), protocol.Publication{
			Sequence: 1,
			Topic:    "dev/tasks",
			Agent:    "agent-a",
			Records:  []protocol.Record{{Index: 1, Text: "hello"}},
		}); err != nil {
			t.Fatal(err)
		}

		err := validateV1Stage(stage, []protocol.Agent{agent}, migration, report)
		if err == nil || !strings.Contains(err.Error(), `obsolete protocol-v4 artifact "history"`) {
			t.Fatalf("validation error = %v", err)
		}
	})

	t.Run("malformed canonical history", func(t *testing.T) {
		stage := filepath.Join(t.TempDir(), "stage")
		if err := initializeV4Root(stage); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(stage, "registry", "agent-a.json"), agent); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(stage, "topics", "dev", "tasks", "history.jsonl"), "{not-json}\n")

		err := validateV1Stage(stage, []protocol.Agent{agent}, migration, report)
		if err == nil || !strings.Contains(err.Error(), `read topic "dev/tasks" through protocol-v4 full read`) {
			t.Fatalf("validation error = %v", err)
		}
	})
}

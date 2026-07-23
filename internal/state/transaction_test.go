package state

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStagedOperationsUseStableRecordIndexAndOverlay(t *testing.T) {
	home := t.TempDir()
	first := newTestClient(t, home, "")
	if _, err := first.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if err := first.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	second := newTestClient(t, home, "writer")
	created, err := second.StagePut("initial")
	if err != nil {
		t.Fatal(err)
	}
	edited, err := second.StageEdit(created.Index, "updated")
	if err != nil || edited.Index != created.Index {
		t.Fatalf("edited = %#v, error = %v", edited, err)
	}
	struck, err := second.StageStrike(created.Index)
	if err != nil || struck.Text != "~~updated~~" {
		t.Fatalf("struck = %#v, error = %v", struck, err)
	}
	publication, err := second.Commit()
	if err != nil || publication.Topic != "dev/tasks" || len(publication.Records) != 1 || publication.Records[0] != struck {
		t.Fatalf("publication = %#v, error = %v", publication, err)
	}
}

func TestOneShotOperationsAndLiteralStrike(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	record, err := client.Put("messages/team", "")
	if err != nil {
		t.Fatal(err)
	}
	record, err = client.Strike("messages/team", record.Index)
	if err != nil || record.Text != "~~~~" {
		t.Fatalf("first strike = %#v, error = %v", record, err)
	}
	record, err = client.Strike("messages/team", record.Index)
	if err != nil || record.Text != "~~~~~~"+"~~" {
		t.Fatalf("second strike = %#v, error = %v", record, err)
	}
	record, err = client.Edit("messages/team", record.Index, "exact")
	if err != nil || record.Text != "exact" {
		t.Fatalf("edit = %#v, error = %v", record, err)
	}
}

func TestTopicLocalIndexesAndAbortGap(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	a, err := client.Put("group/alpha", "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := client.Put("group/beta", "b")
	if err != nil {
		t.Fatal(err)
	}
	if a.Index != 1 || b.Index != 1 {
		t.Fatalf("topic-local indexes = %d and %d", a.Index, b.Index)
	}
	if err := client.Begin("group/alpha"); err != nil {
		t.Fatal(err)
	}
	aborted, err := client.StagePut("aborted")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Abort(); err != nil {
		t.Fatal(err)
	}
	next, err := client.Put("group/alpha", "next")
	if err != nil || next.Index <= aborted.Index {
		t.Fatalf("next = %#v, aborted = %#v, error = %v", next, aborted, err)
	}
	full, err := client.Get(ReadRequest{Topic: "group/alpha", Mode: ReadFull})
	if err != nil || len(full.Records) != 2 {
		t.Fatalf("full = %#v, error = %v", full, err)
	}
}

func TestMissingOperationsDoNotLeakOneShotLease(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Edit("dev/tasks", 1, "missing"); err == nil {
		t.Fatal("missing edit succeeded")
	} else {
		assertCode(t, err, "NOT_FOUND")
	}
	if _, err := client.Strike("dev/tasks", 99); err == nil {
		t.Fatal("missing strike succeeded")
	} else {
		assertCode(t, err, "NOT_FOUND")
	}
	history, err := client.readHistory("dev/tasks")
	if err != nil || len(history) != 0 {
		t.Fatalf("rejected operations published history: %#v, %v", history, err)
	}
	if _, err := client.Put("dev/tasks", "works after failure"); err != nil {
		t.Fatalf("failed one-shot leaked lease: %v", err)
	}
}

func TestSameIndexInOtherTopicIsIndependent(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	alpha, err := client.Put("group/alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := client.Put("group/beta", "beta")
	if err != nil || beta.Index != alpha.Index {
		t.Fatalf("beta = %#v, alpha = %#v, %v", beta, alpha, err)
	}
	if _, err := client.Edit("group/alpha", alpha.Index, "changed"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Strike("group/alpha", alpha.Index); err != nil {
		t.Fatal(err)
	}
	full, err := client.Get(ReadRequest{Topic: "group/beta", Mode: ReadFull})
	if err != nil || len(full.Records) != 1 || full.Records[0] != beta {
		t.Fatalf("other topic changed: %#v, %v", full, err)
	}
}

func TestTextIsPreservedExactly(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"   ", "first\nsecond", "שלום 🌍"} {
		record, err := client.Put("messages/exact", text)
		if err != nil || record.Text != text {
			t.Fatalf("put %#v = %#v, %v", text, record, err)
		}
		edited := text + "\nchanged"
		record, err = client.Edit("messages/exact", record.Index, edited)
		if err != nil || record.Text != edited {
			t.Fatalf("edit %#v = %#v, %v", edited, record, err)
		}
	}
}

func TestRecordAllocatorRejectsMissingRegressedAndExhaustedState(t *testing.T) {
	for name, allocator := range map[string]*int64{
		"missing":   nil,
		"regressed": int64Pointer(0),
		"exhausted": int64Pointer(math.MaxInt64),
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			client := newTestClient(t, home, "")
			if _, err := client.Join("writer", "records"); err != nil {
				t.Fatal(err)
			}
			first, err := client.Put("dev/tasks", "first")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "topics", "dev", "tasks", "record-index.json")
			if allocator == nil {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := writeJSONAtomic(path, map[string]int64{"index": *allocator}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Put("dev/tasks", "must fail"); err == nil {
				t.Fatal("create accepted inconsistent allocator")
			}
			full, err := client.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
			if err != nil || len(full.Records) != 1 || full.Records[0] != first {
				t.Fatalf("current records changed: %#v, %v", full, err)
			}
		})
	}
}

func TestRecoveryPublishesDurableRecordOverlayOnce(t *testing.T) {
	home := t.TempDir()
	owner := newTestClient(t, home, "")
	if _, err := owner.Join("owner", "records"); err != nil {
		t.Fatal(err)
	}
	rescuer := newTestClient(t, home, "")
	if _, err := rescuer.Join("rescuer", "records"); err != nil {
		t.Fatal(err)
	}
	base, err := owner.Put("dev/tasks", "base")
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	created, err := owner.StagePut("created")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.StageEdit(base.Index, "edited"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.StageStrike(base.Index); err != nil {
		t.Fatal(err)
	}
	var lock Lock
	if err := readJSON(owner.writeLockPath("dev/tasks"), &lock); err != nil {
		t.Fatal(err)
	}
	lock.PID = math.MaxInt32
	lock.ProcessStart = "dead-owner"
	lock.Timestamp = time.Now().Add(-2 * owner.LockTimeout)
	lock.TimeoutSec = 1
	if err := writeJSONAtomic(owner.writeLockPath("dev/tasks"), lock); err != nil {
		t.Fatal(err)
	}
	rescuer.Agent = "rescuer"
	next, err := rescuer.Put("dev/tasks", "after recovery")
	if err != nil {
		t.Fatal(err)
	}
	if next.Index <= created.Index {
		t.Fatalf("allocator reused recovered index: created=%d next=%d", created.Index, next.Index)
	}
	full, err := rescuer.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(full.Records)
	want := `[{"index":1,"text":"~~edited~~"},{"index":2,"text":"created"},{"index":3,"text":"after recovery"}]`
	if string(encoded) != want {
		t.Fatalf("recovered records = %s, want %s", encoded, want)
	}
	history, err := rescuer.readHistory("dev/tasks")
	if err != nil || len(history) != 3 {
		t.Fatalf("history = %#v, %v", history, err)
	}
}

func int64Pointer(value int64) *int64 { return &value }

func TestStagedFailureLeavesOwnedTransactionUnchanged(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if err := client.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StageStrike(99); err == nil {
		t.Fatal("missing strike succeeded")
	}
	if _, err := client.StagePut("still open"); err != nil {
		t.Fatalf("transaction did not remain usable: %v", err)
	}
	if _, err := client.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionCannotChangeAfterCommitStarts(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if err := client.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StagePut("value"); err != nil {
		t.Fatal(err)
	}
	var txn Transaction
	if err := readJSON(client.transactionPath("writer"), &txn); err != nil {
		t.Fatal(err)
	}
	txn.Sequence = 99
	if err := writeJSONAtomic(client.transactionPath("writer"), txn); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StagePut("changed"); err == nil {
		t.Fatal("stage succeeded after commit started")
	} else {
		assertCode(t, err, "LOCKED")
	}
}

func TestOneShotReturnsRecordWhenSettlementFailsAfterVisibility(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	// Registration itself already consumes global sequences 1 (the
	// collaboration/agents roster commit) and 2 (join); this Put's own event
	// lands at 3.
	eventPath := filepath.Join(home, "events", indexName(3))
	if err := os.Mkdir(eventPath, 0o700); err != nil {
		t.Fatal(err)
	}
	record, err := client.Put("dev/tasks", "visible")
	if err == nil || record.Index != 1 || record.Text != "visible" {
		t.Fatalf("result = %#v, error = %v", record, err)
	}
	full, readErr := client.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
	if readErr == nil {
		t.Fatal("read unexpectedly bypassed unsettled transaction")
	}
	if len(full.Records) != 0 {
		t.Fatalf("failed read returned records: %#v", full)
	}
	history, historyErr := client.readHistory("dev/tasks")
	if historyErr != nil || len(history) != 1 || history[0].Records[0] != record {
		t.Fatalf("authoritative history = %#v, %v", history, historyErr)
	}
}

func TestOneShotReturnsNoRecordWhenCommitFailsBeforeVisibility(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(filepath.Join(home, "state", "index.json"), map[string]int64{"index": math.MaxInt64}); err != nil {
		t.Fatal(err)
	}
	record, err := client.Put("dev/tasks", "not visible")
	if err == nil || record != (Record{}) {
		t.Fatalf("result = %#v, error = %v", record, err)
	}
	history, historyErr := client.readHistory("dev/tasks")
	if historyErr != nil || len(history) != 0 {
		t.Fatalf("history = %#v, %v", history, historyErr)
	}
}

func TestTextIdenticalEditStillPublishes(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	record, err := client.Put("dev/tasks", "same")
	if err != nil {
		t.Fatal(err)
	}
	edited, err := client.Edit("dev/tasks", record.Index, record.Text)
	if err != nil || edited != record {
		t.Fatalf("edit = %#v, %v", edited, err)
	}
	history, err := client.readHistory("dev/tasks")
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %#v, %v", history, err)
	}
}

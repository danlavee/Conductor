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
	dispatch, err := client.Put("messages/team", "")
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err = client.Strike("messages/team", dispatch.Record().Index)
	if err != nil || dispatch.Record().Text != "~~~~" {
		t.Fatalf("first strike = %#v, error = %v", dispatch, err)
	}
	dispatch, err = client.Strike("messages/team", dispatch.Record().Index)
	if err != nil || dispatch.Record().Text != "~~~~~~"+"~~" {
		t.Fatalf("second strike = %#v, error = %v", dispatch, err)
	}
	dispatch, err = client.Edit("messages/team", dispatch.Record().Index, "exact")
	if err != nil || dispatch.Record().Text != "exact" {
		t.Fatalf("edit = %#v, error = %v", dispatch, err)
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
	if a.Record().Index != 1 || b.Record().Index != 1 {
		t.Fatalf("topic-local indexes = %d and %d", a.Record().Index, b.Record().Index)
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
	if err != nil || next.Record().Index <= aborted.Index {
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
	if err != nil || beta.Record().Index != alpha.Record().Index {
		t.Fatalf("beta = %#v, alpha = %#v, %v", beta, alpha, err)
	}
	if _, err := client.Edit("group/alpha", alpha.Record().Index, "changed"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Strike("group/alpha", alpha.Record().Index); err != nil {
		t.Fatal(err)
	}
	full, err := client.Get(ReadRequest{Topic: "group/beta", Mode: ReadFull})
	if err != nil || len(full.Records) != 1 || full.Records[0] != beta.Record() {
		t.Fatalf("other topic changed: %#v, %v", full, err)
	}
}

func TestTextIsPreservedExactly(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"   ", "first\nsecond", "שלום 🌍"} {
		dispatch, err := client.Put("messages/exact", text)
		if err != nil || dispatch.Record().Text != text {
			t.Fatalf("put %#v = %#v, %v", text, dispatch, err)
		}
		edited := text + "\nchanged"
		dispatch, err = client.Edit("messages/exact", dispatch.Record().Index, edited)
		if err != nil || dispatch.Record().Text != edited {
			t.Fatalf("edit %#v = %#v, %v", edited, dispatch, err)
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
			if err != nil || len(full.Records) != 1 || full.Records[0] != first.Record() {
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
	if _, err := owner.StageEdit(base.Record().Index, "edited"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.StageStrike(base.Record().Index); err != nil {
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
	if next.Record().Index <= created.Index {
		t.Fatalf("allocator reused recovered index: created=%d next=%d", created.Index, next.Record().Index)
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
	dispatch, err := client.Put("dev/tasks", "visible")
	record := dispatch.Record()
	if err == nil || record.Index != 1 || record.Text != "visible" {
		t.Fatalf("result = %#v, error = %v", dispatch, err)
	}
	// The publication landed and reached nobody: the one agent here is
	// registered but subscribed to nothing. That pair is the whole point of
	// answering with a dispatch -- exit 0 on a record no one will read.
	if len(dispatch.Recipients) != 0 {
		t.Fatalf("recipients = %#v; want none, nobody is subscribed", dispatch.Recipients)
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
	dispatch, err := client.Put("dev/tasks", "not visible")
	// Sequence zero is what the CLI branches on to decide the record never
	// became visible, so the contract is pinned there and not only on Record.
	if err == nil || dispatch.Sequence != 0 || dispatch.Record() != (Record{}) {
		t.Fatalf("result = %#v, error = %v", dispatch, err)
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
	edited, err := client.Edit("dev/tasks", record.Record().Index, record.Record().Text)
	if err != nil || edited.Record() != record.Record() {
		t.Fatalf("edit = %#v, %v", edited, err)
	}
	history, err := client.readHistory("dev/tasks")
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %#v, %v", history, err)
	}
}

// A publisher subscribed to its own topic is the shape the whole transaction
// exists for: it looks addressed and reaches nobody. The dispatch must not hand
// the publisher its own name back, or an agent alone on a topic it created
// reads a non-empty audience and stops there.
func TestDispatchNeverNamesThePublisher(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.SubscribeTopic("solo/notes"); err != nil {
		t.Fatal(err)
	}
	alone, err := writer.Put("solo/notes", "anyone there")
	if err != nil {
		t.Fatal(err)
	}
	if len(alone.Recipients) != 0 {
		t.Fatalf("recipients = %#v; want none -- the publisher is not its own audience", alone.Recipients)
	}

	// The same act with a peer subscribed names the peer and only the peer,
	// so the emptiness above is the exclusion and not a broken lookup.
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("solo/notes"); err != nil {
		t.Fatal(err)
	}
	heard, err := writer.Put("solo/notes", "still there?")
	if err != nil {
		t.Fatal(err)
	}
	if len(heard.Recipients) != 1 || heard.Recipients[0] != "reader" {
		t.Fatalf("recipients = %#v; want exactly the peer", heard.Recipients)
	}

	// Broadcast topics take the same rule: every registered agent but the one
	// publishing.
	broadcast, err := writer.Put("collaboration/rules", "a rule")
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcast.Recipients) != 1 || broadcast.Recipients[0] != "reader" {
		t.Fatalf("broadcast recipients = %#v; want every registered agent but the publisher", broadcast.Recipients)
	}
}

// A redaction that removed records and then failed to announce them must still
// answer with its sequence. The skill tells agents that an answer with no
// sequence means nothing was removed, and this is the one path that could make
// that false: the history rewrite is already irreversible when the announcement
// runs.
func TestRedactReportsTheRemovalWhenSettlementFails(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Put("dev/tasks", "doomed"); err != nil {
		t.Fatal(err)
	}
	// Registration consumes global sequences 1 and 2, the Put's event takes 3,
	// so the redaction below reserves 4. A directory there fails the event write
	// after the records are already gone.
	if err := os.Mkdir(filepath.Join(home, "events", indexName(4)), 0o700); err != nil {
		t.Fatal(err)
	}
	dispatch, err := client.Redact("dev/tasks", 1, 1)
	if err == nil {
		t.Fatal("expected the announcement to fail")
	}
	if dispatch.Sequence != 4 {
		t.Fatalf("dispatch = %#v; want the reserved sequence, not the answer for a range that matched nothing", dispatch)
	}
	if dispatch.Recipients == nil || len(dispatch.Recipients) != 0 {
		t.Fatalf("recipients = %#v; want an empty list -- nobody was woken, and the field must not go absent", dispatch.Recipients)
	}
	history, err := client.readHistory("dev/tasks")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history {
		if len(entry.Records) != 0 {
			t.Fatalf("record survived a redaction that reported its sequence: %#v", entry)
		}
	}
}

func TestClientRedact(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	// A second agent, subscribed, so the redaction below has somebody to reach.
	// It cannot be the redacting client itself: an agent is never woken by its
	// own act, so subscribing the writer would leave the announcement reaching
	// nobody, which is a different assertion.
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}

	// 1. Publish three records
	r1, err := client.Put("dev/tasks", "task 1")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := client.Put("dev/tasks", "task 2")
	if err != nil {
		t.Fatal(err)
	}
	r3, err := client.Put("dev/tasks", "task 3")
	if err != nil {
		t.Fatal(err)
	}

	// Verify initial materialized state
	full, err := client.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(full.Records))
	}
	if full.Records[0].Index != r1.Record().Index || full.Records[1].Index != r2.Record().Index || full.Records[2].Index != r3.Record().Index {
		t.Fatal("records out of index sequence order")
	}

	// 2. Redact single record (Record 2)
	redaction, err := client.Redact("dev/tasks", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	// A redaction that matched something says so: a sequence, and who was told
	// it happened. Nothing else in the API reports that a removal was announced.
	if redaction.Sequence == 0 || len(redaction.Recipients) == 0 {
		t.Fatalf("redaction = %#v; want a sequence and the agents told", redaction)
	}

	// 3. Verify record 2 is gone, but 1 and 3 are present (indices MUST not shift)
	fullRedacted, err := client.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(fullRedacted.Records) != 2 {
		t.Fatalf("expected 2 records after redaction, got %d", len(fullRedacted.Records))
	}
	if fullRedacted.Records[0].Index != 1 || fullRedacted.Records[0].Text != "task 1" {
		t.Fatalf("record 1 shifted or changed: %#v", fullRedacted.Records[0])
	}
	if fullRedacted.Records[1].Index != 3 || fullRedacted.Records[1].Text != "task 3" {
		t.Fatalf("record 3 shifted or changed: %#v", fullRedacted.Records[1])
	}

	// 4. Verify that automatic backup file exists in the topic directory
	dir, err := client.topicDir("dev/tasks")
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "history.jsonl.bak-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 backup file, found %d", len(matches))
	}
	// Verify backup contains the original unredacted history
	backupData, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(backupData) == 0 {
		t.Fatal("backup file is empty")
	}

	// 5. Redact remaining range [1, 3]
	if _, err = client.Redact("dev/tasks", 1, 3); err != nil {
		t.Fatal(err)
	}
	// The same range again matches nothing, and the zero dispatch is the only
	// thing that distinguishes that from the redaction just above.
	repeat, err := client.Redact("dev/tasks", 1, 3)
	if err != nil || repeat.Sequence != 0 {
		t.Fatalf("repeat redaction = %#v, %v; want nothing removed and nobody told", repeat, err)
	}

	// Verify all records in the range are completely pruned from live history
	fullCleared, err := client.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(fullCleared.Records) != 0 {
		t.Fatalf("expected 0 records, got %d", len(fullCleared.Records))
	}

	// Now there should be exactly two backup files (one from each redaction)
	matchesAfter, err := filepath.Glob(filepath.Join(dir, "history.jsonl.bak-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matchesAfter) != 2 {
		t.Fatalf("expected exactly 2 backup files, found %d", len(matchesAfter))
	}
}


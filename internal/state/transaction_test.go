package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTransactionSurvivesSeparateClients(t *testing.T) {
	home := t.TempDir()
	first := newTestClient(t, home, "")
	if _, err := first.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	if err := first.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}

	second := newTestClient(t, home, "writer")
	if err := second.Set("task-42", "task", "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "transactions", "writer.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transaction file remains after commit: %v", err)
	}
}

func TestConcurrentSameAgentSetsAreSerialized(t *testing.T) {
	home := t.TempDir()
	owner := newTestClient(t, home, "")
	if _, err := owner.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for key, value := range map[string]string{"alpha": "one", "beta": "two"} {
		go func(key, value string) {
			client := newTestClient(t, home, "writer")
			results <- client.Set(key, "test", value)
		}(key, value)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	change, err := owner.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Messages) != 2 {
		t.Fatalf("concurrent set lost a message: %#v", change.Messages)
	}
}

func TestConditionalEditRejectsStaleMessageIndex(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	created, err := client.Put("dev/tasks", map[string]MessageMutation{"task-42": testMessage("old")})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := client.PutWithOptions(
		"dev/tasks",
		map[string]MessageMutation{"task-42": testMessage("new")},
		WriteOptions{IfIndex: map[string]int64{"task-42": created.Index}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var indexState struct {
		Index int64 `json:"index"`
	}
	if err := readJSON(filepath.Join(home, "state", "index.json"), &indexState); err != nil {
		t.Fatal(err)
	}
	_, err = client.PutWithOptions(
		"dev/tasks",
		map[string]MessageMutation{"task-42": testMessage("stale")},
		WriteOptions{IfIndex: map[string]int64{"task-42": created.Index}},
	)
	var protocol *ProtocolError
	if !errors.As(err, &protocol) || protocol.Code != "CONFLICT" || protocol.Conflict == nil {
		t.Fatalf("error = %#v", err)
	}
	detail := protocol.Conflict
	if detail.Resource != "dev/tasks" || detail.Key != "task-42" || detail.ExpectedIndex != created.Index || detail.CurrentIndex != updated.Index {
		t.Fatalf("conflict = %+v", detail)
	}
	var afterConflict struct {
		Index int64 `json:"index"`
	}
	if err := readJSON(filepath.Join(home, "state", "index.json"), &afterConflict); err != nil {
		t.Fatal(err)
	}
	if afterConflict.Index != indexState.Index {
		t.Fatalf("conflict advanced global index from %d to %d", indexState.Index, afterConflict.Index)
	}
	if _, err := os.Stat(client.transactionPath("writer")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected write left a transaction: %v", err)
	}
	if _, err := os.Stat(client.writeGuardPath("dev/tasks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected write left a resource guard: %v", err)
	}
	if _, err := os.Stat(client.writeLockPath("dev/tasks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected write left a resource lock: %v", err)
	}
	history, err := client.readHistory("dev/tasks")
	if err != nil || len(history) != 2 || history[len(history)-1].Index != updated.Index {
		t.Fatalf("history changed after conflict: %#v, %v", history, err)
	}
	var head struct {
		Index int64 `json:"index"`
	}
	if err := readJSON(filepath.Join(home, "topics", "dev", "tasks", "head.json"), &head); err != nil || head.Index != updated.Index {
		t.Fatalf("head after conflict = %+v, %v", head, err)
	}
	events, err := os.ReadDir(filepath.Join(home, "events"))
	if err != nil || len(events) != 3 {
		t.Fatalf("events after conflict = %d, %v", len(events), err)
	}
	inbox, err := os.ReadFile(filepath.Join(home, "inbox", "writer"))
	if err != nil || bytes.Count(inbox, []byte{'\n'}) != 3 {
		t.Fatalf("inbox after conflict = %q, %v", inbox, err)
	}
	full, err := client.Get(ReadRequest{Resource: "dev/tasks", Key: "task-42", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Messages) != 1 || full.Messages[0].Index != updated.Index || full.Messages[0].Payload.Text != "new" {
		t.Fatalf("full result = %#v", full)
	}
}

func TestConditionalWriteSupportsCreateAndPerKeyIndexes(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	alpha, err := client.PutWithOptions(
		"dev/tasks",
		map[string]MessageMutation{"alpha": testMessage("one")},
		WriteOptions{IfIndex: map[string]int64{"alpha": 0}},
	)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := client.Put("dev/tasks", map[string]MessageMutation{"beta": testMessage("two")})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := client.PutWithOptions(
		"dev/tasks",
		map[string]MessageMutation{"alpha": testMessage("updated")},
		WriteOptions{IfIndex: map[string]int64{"alpha": alpha.Index, "beta": beta.Index}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Index <= beta.Index {
		t.Fatalf("conditional update index = %d", changed.Index)
	}
	sameValue, err := client.PutWithOptions(
		"dev/tasks",
		map[string]MessageMutation{"alpha": testMessage("updated")},
		WriteOptions{IfIndex: map[string]int64{"alpha": changed.Index}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if sameValue.Index <= changed.Index {
		t.Fatalf("same-value replacement did not publish: %d", sameValue.Index)
	}
	_, err = client.PutWithOptions(
		"dev/tasks",
		map[string]MessageMutation{"alpha": testMessage("again")},
		WriteOptions{IfIndex: map[string]int64{"alpha": 0}},
	)
	var protocol *ProtocolError
	if !errors.As(err, &protocol) || protocol.Conflict == nil || protocol.Conflict.ExpectedIndex != 0 || protocol.Conflict.CurrentIndex != sameValue.Index {
		t.Fatalf("create conflict = %#v", err)
	}
}

func TestConflictEnvelopePreservesZeroIndexes(t *testing.T) {
	payload, err := json.Marshal(&ProtocolError{
		Code: "CONFLICT",
		Text: "message changed since it was read",
		Conflict: &ConflictDetail{
			Resource:      "dev/tasks",
			Key:           "task-42",
			ExpectedIndex: 0,
			CurrentIndex:  0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	conflict, ok := envelope["conflict"].(map[string]any)
	if !ok || conflict["expected_index"] != float64(0) || conflict["current_index"] != float64(0) {
		t.Fatalf("conflict envelope = %s", payload)
	}
}

func TestConditionalStagedWriteAndStaleBegin(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	created, err := client.Put("dev/tasks", map[string]MessageMutation{"task": testMessage("old")})
	if err != nil {
		t.Fatal(err)
	}
	options := WriteOptions{IfIndex: map[string]int64{"task": created.Index}}
	if err := client.BeginWithOptions("dev/tasks", options); err != nil {
		t.Fatal(err)
	}
	if err := client.Set("task", "test", "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Commit(); err != nil {
		t.Fatal(err)
	}
	assertCode(t, client.BeginWithOptions("dev/tasks", options), "CONFLICT")
}

func TestConditionalAdmissionFailsClosedWhenHistoryIsIncomplete(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	alpha, err := client.Put("dev/tasks", map[string]MessageMutation{"alpha": testMessage("one")})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := client.Put("dev/tasks", map[string]MessageMutation{"beta": testMessage("two")})
	if err != nil {
		t.Fatal(err)
	}
	historyPath := filepath.Join(home, "topics", "dev", "tasks", "history", indexName(alpha.Index))
	if err := os.Remove(historyPath); err != nil {
		t.Fatal(err)
	}
	_, err = client.PutWithOptions(
		"dev/tasks",
		map[string]MessageMutation{"alpha": testMessage("unsafe")},
		WriteOptions{IfIndex: map[string]int64{"alpha": 0}},
	)
	if err == nil {
		t.Fatal("conditional write succeeded with incomplete authoritative history")
	}
	var indexState struct {
		Index int64 `json:"index"`
	}
	if err := readJSON(filepath.Join(home, "state", "index.json"), &indexState); err != nil || indexState.Index != beta.Index {
		t.Fatalf("index after rejected corrupt state = %+v, %v", indexState, err)
	}
}

func TestSameAgentRecoversExpiredPreTransactionLease(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.acquireWrite("dev/tasks", "writer"); err != nil {
		t.Fatal(err)
	}
	var abandoned Lock
	if err := readJSON(client.writeLockPath("dev/tasks"), &abandoned); err != nil {
		t.Fatal(err)
	}
	abandoned.PID = 999999
	abandoned.ProcessStart = "dead"
	abandoned.Timestamp = time.Now().Add(-time.Minute)
	abandoned.TimeoutSec = 1
	if err := writeJSONAtomic(client.writeLockPath("dev/tasks"), abandoned); err != nil {
		t.Fatal(err)
	}
	if err := client.BeginWithOptions("dev/tasks", WriteOptions{IfIndex: map[string]int64{"task": 0}}); err != nil {
		t.Fatal(err)
	}
	if err := client.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanRecoveryPreservesTransactionForAnotherResource(t *testing.T) {
	home := t.TempDir()
	owner := newTestClient(t, home, "")
	if _, err := owner.Register("owner", "development"); err != nil {
		t.Fatal(err)
	}
	recoverer := newTestClient(t, home, "")
	if _, err := recoverer.Register("recoverer", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.acquireWrite("dev/orphan", "owner"); err != nil {
		t.Fatal(err)
	}
	expireTestWriter(t, owner, "dev/orphan")
	if err := owner.Begin("dev/active"); err != nil {
		t.Fatal(err)
	}
	if _, err := recoverer.Get(ReadRequest{Resource: "dev/orphan", Mode: ReadFull}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Set("task", "test", "preserved"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Commit(); err != nil {
		t.Fatal(err)
	}
	result, err := recoverer.Get(ReadRequest{Resource: "dev/active", Key: "task", Mode: ReadFull})
	if err != nil || len(result.Messages) != 1 || result.Messages[0].Payload.Text != "preserved" {
		t.Fatalf("active transaction = %#v, %v", result, err)
	}
}

func TestConditionalCheckSeesRecoveredPredecessor(t *testing.T) {
	home := t.TempDir()
	owner := newTestClient(t, home, "")
	if _, err := owner.Register("owner", "development"); err != nil {
		t.Fatal(err)
	}
	contender := newTestClient(t, home, "")
	if _, err := contender.Register("contender", "review"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Set("task", "test", "predecessor"); err != nil {
		t.Fatal(err)
	}
	expireTestWriter(t, owner, "dev/tasks")
	_, err := contender.PutWithOptions(
		"dev/tasks",
		map[string]MessageMutation{"task": testMessage("contender")},
		WriteOptions{IfIndex: map[string]int64{"task": 0}},
	)
	assertCode(t, err, "CONFLICT")
	history, err := contender.readHistory("dev/tasks")
	if err != nil || len(history) != 1 || history[0].Messages["task"].Payload.Text != "predecessor" {
		t.Fatalf("recovered history = %#v, %v", history, err)
	}
}

func TestAcceptedConditionalTransactionRecoversWithoutRecheck(t *testing.T) {
	home := t.TempDir()
	owner := newTestClient(t, home, "")
	if _, err := owner.Register("owner", "development"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Register("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if err := owner.BeginWithOptions("dev/tasks", WriteOptions{IfIndex: map[string]int64{"task": 0}}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Set("task", "test", "accepted"); err != nil {
		t.Fatal(err)
	}
	expireTestWriter(t, owner, "dev/tasks")
	result, err := reader.Get(ReadRequest{Resource: "dev/tasks", Key: "task", Mode: ReadFull})
	if err != nil || len(result.Messages) != 1 || result.Messages[0].Payload.Text != "accepted" {
		t.Fatalf("recovered record = %#v, %v", result, err)
	}
}

func expireTestWriter(t *testing.T, client *Client, resource string) {
	t.Helper()
	var lock Lock
	if err := readJSON(client.writeLockPath(resource), &lock); err != nil {
		t.Fatal(err)
	}
	lock.PID = 999999
	lock.ProcessStart = "dead"
	lock.Timestamp = time.Now().Add(-time.Minute)
	lock.TimeoutSec = 1
	if err := writeJSONAtomic(client.writeLockPath(resource), lock); err != nil {
		t.Fatal(err)
	}
}

func TestConditionalMismatchIsDeterministicAndAtomic(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	baseline, err := client.Put("dev/tasks", map[string]MessageMutation{"alpha": testMessage("one"), "zeta": testMessage("two")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.PutWithOptions(
		"dev/tasks",
		map[string]MessageMutation{"alpha": testMessage("changed"), "zeta": testMessage("changed")},
		WriteOptions{IfIndex: map[string]int64{"zeta": baseline.Index - 1, "alpha": baseline.Index - 1}},
	)
	var protocol *ProtocolError
	if !errors.As(err, &protocol) || protocol.Conflict == nil || protocol.Conflict.Key != "alpha" {
		t.Fatalf("deterministic conflict = %#v", err)
	}
	full, err := client.Get(ReadRequest{Resource: "dev/tasks", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Messages) != 2 || full.Messages[0].Index != baseline.Index || full.Messages[1].Index != baseline.Index {
		t.Fatalf("rejected multi-write changed state: %#v", full.Messages)
	}
}

func TestTransactionCannotChangeAfterCommitStarts(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	if err := client.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if err := client.Set("task", "test", "value"); err != nil {
		t.Fatal(err)
	}
	var transaction Transaction
	if err := readJSON(client.transactionPath("writer"), &transaction); err != nil {
		t.Fatal(err)
	}
	transaction.Index = 99
	if err := writeJSONAtomic(client.transactionPath("writer"), transaction); err != nil {
		t.Fatal(err)
	}
	assertCode(t, client.Set("task", "test", "changed"), "LOCKED")
	assertCode(t, client.Abort(), "LOCKED")
	transaction.Index = 0
	if err := writeJSONAtomic(client.transactionPath("writer"), transaction); err != nil {
		t.Fatal(err)
	}
	if err := client.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredTransactionFlushesBeforeTakeover(t *testing.T) {
	home := t.TempDir()
	a := newTestClient(t, home, "")
	if _, err := a.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	b := newTestClient(t, home, "")
	if _, err := b.Register("b", "review"); err != nil {
		t.Fatal(err)
	}
	if err := a.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("from-a", "test", "staged"); err != nil {
		t.Fatal(err)
	}
	var abandoned Lock
	if err := readJSON(a.writeLockPath("dev/tasks"), &abandoned); err != nil {
		t.Fatal(err)
	}
	abandoned.PID = 999999
	abandoned.ProcessStart = "dead"
	abandoned.Timestamp = time.Now().Add(-time.Minute)
	abandoned.TimeoutSec = 1
	if err := writeJSONAtomic(a.writeLockPath("dev/tasks"), abandoned); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put("dev/tasks", map[string]MessageMutation{"from-b": testMessage("done")}); err != nil {
		t.Fatal(err)
	}
	full, err := b.Get(ReadRequest{Resource: "dev/tasks", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Messages) != 2 {
		t.Fatalf("recovery did not flush then apply: %#v", full.Messages)
	}
}

func TestReadFlushesExpiredDeadTransaction(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Register("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Set("status", "test", "staged"); err != nil {
		t.Fatal(err)
	}
	var abandoned Lock
	if err := readJSON(writer.writeLockPath("dev/tasks"), &abandoned); err != nil {
		t.Fatal(err)
	}
	abandoned.PID = 999999
	abandoned.ProcessStart = "dead"
	abandoned.Timestamp = time.Now().Add(-time.Minute)
	abandoned.TimeoutSec = 1
	if err := writeJSONAtomic(writer.writeLockPath("dev/tasks"), abandoned); err != nil {
		t.Fatal(err)
	}
	result, err := reader.Get(ReadRequest{Resource: "dev/tasks", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Payload.Text != "staged" {
		t.Fatalf("read did not recover buffered write: %#v", result)
	}
}

func TestDeregisterRequiresTransactionResolution(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Register("writer", "development"); err != nil {
		t.Fatal(err)
	}
	if err := client.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	assertCode(t, client.Deregister("writer"), "LOCKED")
	if err := client.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := client.Deregister("writer"); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredLiveOwnerReturnsTimeout(t *testing.T) {
	home := t.TempDir()
	a := newTestClient(t, home, "")
	if _, err := a.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	b := newTestClient(t, home, "")
	if _, err := b.Register("b", "review"); err != nil {
		t.Fatal(err)
	}
	if err := a.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	var live Lock
	if err := readJSON(a.writeLockPath("dev/tasks"), &live); err != nil {
		t.Fatal(err)
	}
	live.Timestamp = time.Now().Add(-time.Minute)
	live.TimeoutSec = 1
	if err := writeJSONAtomic(a.writeLockPath("dev/tasks"), live); err != nil {
		t.Fatal(err)
	}
	_, err := b.Put("dev/tasks", map[string]MessageMutation{"status": testMessage("done")})
	assertCode(t, err, "TIMEOUT")
	if err := a.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestBeginLeaseTracksTerminalOwnerProcess(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := client.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	var lock Lock
	if err := readJSON(client.writeLockPath("dev/tasks"), &lock); err != nil {
		t.Fatal(err)
	}
	if lock.PID != os.Getppid() {
		t.Fatalf("lease PID = %d, want terminal parent %d", lock.PID, os.Getppid())
	}
	if err := client.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestInitialLeaseTracksCallingProcessUntilTransactionIsDurable(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Register("a", "development"); err != nil {
		t.Fatal(err)
	}
	lock, err := client.acquireWrite("dev/tasks", "a")
	if err != nil {
		t.Fatal(err)
	}
	if lock.PID != os.Getpid() {
		t.Fatalf("initial lease PID = %d, want calling process %d", lock.PID, os.Getpid())
	}
	if err := client.releaseWrite("dev/tasks", "a"); err != nil {
		t.Fatal(err)
	}
}

func TestReaderRecoversOrphanWriterGuard(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("reader", "review"); err != nil {
		t.Fatal(err)
	}
	guard := client.writeGuardPath("dev/tasks")
	if err := os.Mkdir(guard, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(guard, old, old); err != nil {
		t.Fatal(err)
	}
	client.LockTimeout = time.Second
	if _, err := client.Get(ReadRequest{Resource: "dev/tasks", Mode: ReadFull}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(guard); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan guard remains: %v", err)
	}
}

func TestProtocolErrors(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	assertCode(t, client.Set("key", "test", "value"), "NO_BUFFER")
	_, err := client.Commit()
	assertCode(t, err, "NO_LOCK")
	_, err = client.Get(ReadRequest{Resource: "dev/tasks", Key: "missing", Mode: ReadFull})
	assertCode(t, err, "NOT_FOUND")
}

func TestExpiredDeadReaderMarkerIsRecovered(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	readerDir := client.readerLockDir("dev/tasks")
	if err := os.MkdirAll(readerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	reader := Lock{PID: 999999, ProcessStart: "dead", LeaseID: 1, Agent: "dead-reader", Timestamp: time.Now().Add(-time.Minute), TimeoutSec: 1}
	if err := writeJSONAtomic(filepath.Join(readerDir, "orphan.lock"), reader); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Put("dev/tasks", map[string]MessageMutation{"status": testMessage("done")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(readerDir, "orphan.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dead reader marker remains: %v", err)
	}
}

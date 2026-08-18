package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/cutover"
	"github.com/danlavee/Conductor/internal/platform"
)

func TestFrozenCutoverBlocksMutatorsAcknowledgmentsAndInitialization(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	client, err := New(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Join("reader", "review")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AcknowledgeSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	record, err := client.Put("dev/tasks", "before")
	if err != nil {
		t.Fatal(err)
	}
	delta, err := client.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	watch, err := client.WatchResultContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(watch.Summaries) == 0 {
		t.Fatal("setup produced no summary")
	}
	summary := watch.Summaries[len(watch.Summaries)-1]
	if err := watch.Close(); err != nil {
		t.Fatal(err)
	}
	delivery := Delivery{Summary: summary, Mode: DeliverySummary}
	batch := BatchDelivery{Deliveries: []Delivery{delivery}}
	if _, err := cutover.Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	before, err := rootFileSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	assertBlocked := func(name string, err error) {
		t.Helper()
		var blocked *cutover.BlockedError
		if !errors.As(err, &blocked) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
	checks := []struct {
		name string
		call func() error
	}{
		{"join", func() error { _, err := client.Join("reader", ""); return err }},
		{"leave", func() error { return client.Leave("reader") }},
		{"subscribe topic", func() error { _, err := client.SubscribeTopic("dev/tasks"); return err }},
		{"subscribe group", func() error { _, err := client.SubscribeTopicGroup("dev"); return err }},
		{"subscription read", func() error { _, err := client.Subscription(); return err }},
		{"begin", func() error { return client.Begin("dev/tasks") }},
		{"stage put", func() error { _, err := client.StagePut("blocked"); return err }},
		{"stage edit", func() error { _, err := client.StageEdit(record.Index, "blocked"); return err }},
		{"stage strike", func() error { _, err := client.StageStrike(record.Index); return err }},
		{"commit", func() error { _, err := client.Commit(); return err }},
		{"abort", func() error { return client.Abort() }},
		{"put", func() error { _, err := client.Put("dev/tasks", "blocked"); return err }},
		{"edit", func() error { _, err := client.Edit("dev/tasks", record.Index, "blocked"); return err }},
		{"strike", func() error { _, err := client.Strike("dev/tasks", record.Index); return err }},
		{"redact", func() error { return client.Redact("dev/tasks", record.Index, record.Index) }},
		{"get", func() error { _, err := client.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull}); return err }},
		{"snapshot ack", func() error { return client.AcknowledgeSnapshot(snapshot) }},
		{"read ack", func() error { return client.AcknowledgeRead(delta) }},
		{"summary ack", func() error { return client.AcknowledgeSummary(summary) }},
		{"delivery ack", func() error { return client.AcknowledgeDelivery(delivery) }},
		{"batch ack", func() error { return client.AcknowledgeBatch(batch) }},
		{"resolve delivery", func() error { _, err := client.ResolveDelivery(summary, DeliveryContent); return err }},
		{"resolve batch", func() error { _, err := client.ResolveBatch([]Summary{summary}, DeliveryContent); return err }},
		{"list agents", func() error { _, err := client.ListAgents(); return err }},
		{"list groups", func() error { _, err := client.ListTopicGroups(); return err }},
		{"list topics", func() error { _, err := client.ListTopics("dev"); return err }},
	}
	for _, check := range checks {
		assertBlocked(check.name, check.call())
	}
	_, err = New(root, "other")
	assertBlocked("client initialization", err)
	after, err := rootFileSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("frozen operations changed root\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestWatcherStopsReadingRootAndFiresControlOnlyReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	client, err := New(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	drainPending(t, client)
	idle := make(chan struct{}, 1)
	client.watchIdleFn = func() {
		select {
		case idle <- struct{}{}:
		default:
		}
	}
	client.PollInterval = 5 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resultCh := make(chan WatchResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := client.WatchResultContext(ctx)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()
	<-idle
	if _, err := cutover.Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := cutover.MarkReplaced(root, "cut-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		t.Fatalf("watch touched replaced root: %v", err)
	case result := <-resultCh:
		if result.Activation == nil || result.Activation.Type != "conductor-replaced" ||
			result.Activation.CutoverID != "cut-1" || len(result.Summaries) != 0 {
			t.Fatalf("watch result = %#v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestNewWatcherDoesNotDuplicateReplacementActivation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	client, err := New(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	drainPending(t, client)
	if _, err := cutover.Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cutover.MarkReplaced(root, "cut-1"); err != nil {
		t.Fatal(err)
	}
	rearmed, err := Open(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	releaseOwnership, err := rearmed.AcquireWatchOwnership()
	if err != nil {
		t.Fatalf("replacement watcher could not own control-only wait: %v", err)
	}
	defer releaseOwnership()
	rearmed.PollInterval = 5 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	resultCh := make(chan WatchResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := rearmed.WatchResultContext(ctx)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()
	select {
	case result := <-resultCh:
		t.Fatalf("new watcher duplicated activation: %#v", result)
	case err := <-errCh:
		t.Fatalf("new watcher exited before activation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if _, err := cutover.Activate(root, "cut-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-resultCh:
		t.Fatalf("new watcher returned without data: %#v", result)
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("watch error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("new watcher did not rearm into active state")
	}
}

func TestOwnedWatcherCannotMissFreezeBeforeItsFirstRootPoll(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	client, err := New(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	releaseOwnership, err := client.AcquireWatchOwnership()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOwnership()
	if _, err := cutover.Freeze(root, "cut-1", "v5", func() error {
		return ValidateCutoverPreflight(root)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := cutover.MarkReplaced(root, "cut-1"); err != nil {
		t.Fatal(err)
	}
	result, err := client.WatchResultContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Activation == nil || result.Activation.CutoverID != "cut-1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestPendingDeltaSurvivesReplacementActivation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	reader, err := New(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	writer, err := New(root, "writer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Join("writer", "build"); err != nil {
		t.Fatal(err)
	}
	drainPending(t, reader)
	idle := make(chan struct{}, 1)
	reader.watchIdleFn = func() {
		select {
		case idle <- struct{}{}:
		default:
		}
	}
	reader.PollInterval = time.Second
	oldResult := make(chan WatchResult, 1)
	oldErr := make(chan error, 1)
	go func() {
		result, err := reader.WatchResultContext(context.Background())
		if err != nil {
			oldErr <- err
			return
		}
		oldResult <- result
	}()
	<-idle
	if _, err := writer.Put("dev/tasks", "pending"); err != nil {
		t.Fatal(err)
	}
	if _, err := cutover.Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cutover.MarkReplaced(root, "cut-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-oldResult:
		if result.Activation == nil {
			t.Fatalf("old watcher returned delta instead of replacement: %#v", result)
		}
	case err := <-oldErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("old watcher did not fire replacement")
	}
	if _, err := cutover.Activate(root, "cut-1"); err != nil {
		t.Fatal(err)
	}
	rearmed, err := Open(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	rearmed.PollInterval = 5 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := rearmed.WatchResultContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	found := false
	for _, summary := range result.Summaries {
		if summary.Topic == "dev/tasks" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending delta was lost: %#v", result.Summaries)
	}
}

func TestMissingMarkerAfterFreezeObservationFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	client, err := New(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	drainPending(t, client)
	idle := make(chan struct{}, 1)
	client.watchIdleFn = func() {
		select {
		case idle <- struct{}{}:
		default:
		}
	}
	client.PollInterval = 5 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := client.WatchResultContext(ctx)
		errCh <- err
	}()
	<-idle
	if _, err := cutover.Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	dir, err := cutover.Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err == nil || err.Error() != "cutover state is missing" {
			t.Fatalf("watch error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("watch did not fail closed")
	}
}

func TestCutoverPreflightPreservesDurableTransactionAndAbortsFreeze(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	client, err := New(root, "writer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("writer", "build"); err != nil {
		t.Fatal(err)
	}
	if err := client.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StagePut("durable"); err != nil {
		t.Fatal(err)
	}
	state, err := cutover.Freeze(root, "cut-1", "v5", func() error {
		return ValidateCutoverPreflight(root)
	})
	if err == nil || state.Phase != cutover.Active {
		t.Fatalf("freeze = %#v, %v", state, err)
	}
	if _, err := os.Stat(client.transactionPath("writer")); err != nil {
		t.Fatalf("transaction changed by preflight: %v", err)
	}
	if err := client.Abort(); err != nil {
		t.Fatalf("admission was not restored: %v", err)
	}
}

func TestConcurrentWriterCannotCrossFreezeBarrier(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	client, err := New(root, "writer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("writer", "build"); err != nil {
		t.Fatal(err)
	}
	wrote := make(chan struct{}, 1)
	stopped := make(chan error, 1)
	go func() {
		for {
			if _, err := client.Put("dev/tasks", "racing"); err != nil {
				stopped <- err
				return
			}
			select {
			case wrote <- struct{}{}:
			default:
			}
		}
	}()
	select {
	case <-wrote:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	if _, err := cutover.Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		var blocked *cutover.BlockedError
		if !errors.As(err, &blocked) {
			t.Fatalf("writer error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer remained admitted after barrier")
	}
	before, err := rootFileSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	after, err := rootFileSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("root changed after freeze barrier")
	}
}

func TestCutoverPreflightRefusesLegacyWatcherAndAcceptsCapableWatcher(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	client, err := New(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	legacyGuard := filepath.Join(root, "state", "watch", "reader.guard")
	releaseLegacy, err := platform.AcquireFileMutex(legacyGuard, 0)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cutover.Freeze(root, "legacy", "v5", func() error {
		return ValidateCutoverPreflight(root)
	})
	if err == nil || state.Phase != cutover.Active {
		t.Fatalf("legacy freeze = %#v, %v", state, err)
	}
	if err := releaseLegacy(); err != nil {
		t.Fatal(err)
	}

	releaseWatch, err := client.AcquireWatchOwnership()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseWatch()
	state, err = cutover.Freeze(root, "capable", "v5", func() error {
		return ValidateCutoverPreflight(root)
	})
	if err != nil || state.Phase != cutover.Frozen {
		t.Fatalf("capable freeze = %#v, %v", state, err)
	}
}

func waitForNoOperationLease(t *testing.T, root string) {
	t.Helper()
	dir, err := cutover.Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		entries, err := os.ReadDir(filepath.Join(dir, "leases"))
		if errors.Is(err, os.ErrNotExist) || (err == nil && len(entries) == 0) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("watch did not release its operation lease")
		}
		time.Sleep(time.Millisecond)
	}
}

func drainPending(t *testing.T, client *Client) {
	t.Helper()
	result, err := client.WatchResultContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if result.Activation != nil {
		t.Fatalf("unexpected replacement while draining: %#v", result.Activation)
	}
	batch, err := client.ResolveBatch(result.Summaries, DeliverySummary)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AcknowledgeBatch(batch); err != nil {
		t.Fatal(err)
	}
}

func rootFileSnapshot(root string) (string, error) {
	var result string
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
		result += relative + ":" + string(data) + "\n"
		return nil
	})
	return result, err
}

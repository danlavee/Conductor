package state

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseDeliveryMode(t *testing.T) {
	if mode, err := ParseDeliveryMode(""); err != nil || mode != DeliveryContent {
		t.Fatalf("default mode = %v, %v", mode, err)
	}
	if mode, err := ParseDeliveryMode("summary"); err != nil || mode != DeliverySummary {
		t.Fatalf("summary mode = %v, %v", mode, err)
	}
	if _, err := ParseDeliveryMode("bogus"); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestSummaryAcknowledgesReferencedTopicChange(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscribeTopic("messages/team"); err != nil {
		t.Fatal(err)
	}
	// Drain the collaboration/agents roster commit and the join signal
	// registration itself produces before exercising the messages/team delta.
	drainSummaries(t, client, 2)
	if _, err := client.Put("messages/team", "hello"); err != nil {
		t.Fatal(err)
	}
	summary := watchOne(t, client)
	delivery, err := client.ResolveDelivery(summary, DeliverySummary)
	if err != nil || delivery.Delta != nil {
		t.Fatalf("delivery = %#v, error = %v", delivery, err)
	}
	cursorWrites := 0
	client.saveCursorFn = func(agent string, cursor Cursor) error {
		cursorWrites++
		return writeJSONAtomic(filepath.Join(client.Home, "cursors", agent+".json"), cursor)
	}
	if err := client.AcknowledgeDelivery(delivery); err != nil {
		t.Fatal(err)
	}
	if cursorWrites != 1 {
		t.Fatalf("delivery cursor writes = %d, want 1", cursorWrites)
	}
	cursor, err := client.loadCursor("reader")
	if err != nil {
		t.Fatal(err)
	}
	if !indexAcknowledged(cursor.SummaryRanges, summary.Sequence) || cursor.Topics["messages/team"] != summary.Sequence {
		t.Fatalf("delivery was not settled in one cursor state: %#v", cursor)
	}
	delta, err := client.Get(ReadRequest{Topic: "messages/team", Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 0 {
		t.Fatalf("delta = %#v, error = %v", delta, err)
	}
}

func TestContentCarriesChangedRecordsAndAcknowledgesDelta(t *testing.T) {
	home := t.TempDir()
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	// Each registration (reader's own, then writer's, forced-broadcast to
	// every registered agent including reader) produces a collaboration/agents
	// roster commit and a join signal: drain all four before exercising the
	// dev/tasks delta below.
	drainSummaries(t, reader, 4)
	if err := writer.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.StagePut("one"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.StagePut("two"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	summary := watchOne(t, reader)
	delivery, err := reader.ResolveDelivery(summary, DeliveryContent)
	if err != nil || delivery.Delta == nil || len(delivery.Delta.Publications) != 1 || len(delivery.Delta.Publications[0].Records) != 2 {
		t.Fatalf("delivery = %#v, error = %v", delivery, err)
	}
	cursorWrites := 0
	reader.saveCursorFn = func(agent string, cursor Cursor) error {
		cursorWrites++
		return writeJSONAtomic(filepath.Join(reader.Home, "cursors", agent+".json"), cursor)
	}
	if err := reader.AcknowledgeDelivery(delivery); err != nil {
		t.Fatal(err)
	}
	if cursorWrites != 1 {
		t.Fatalf("delivery cursor writes = %d, want 1", cursorWrites)
	}
	cursor, err := reader.loadCursor("reader")
	if err != nil {
		t.Fatal(err)
	}
	if !indexAcknowledged(cursor.SummaryRanges, summary.Sequence) || cursor.Topics["dev/tasks"] != summary.Sequence {
		t.Fatalf("delivery was not settled in one cursor state: %#v", cursor)
	}
	delta, err := reader.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 0 {
		t.Fatalf("delta replayed: %#v, error = %v", delta, err)
	}
}

func TestFailedSummaryDeliveryCursorWriteLeavesSummaryAndDeltaReplayable(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscribeTopic("messages/team"); err != nil {
		t.Fatal(err)
	}
	drainSummaries(t, client, 2)
	if _, err := client.Put("messages/team", "hello"); err != nil {
		t.Fatal(err)
	}
	summaries, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %#v, want one", summaries)
	}
	delivery, err := client.ResolveDelivery(summaries[0], DeliverySummary)
	if err != nil {
		t.Fatal(err)
	}
	saveFailure := errors.New("save failed")
	client.saveCursorFn = func(string, Cursor) error {
		return saveFailure
	}
	if err := client.AcknowledgeDelivery(delivery); !errors.Is(err, saveFailure) {
		t.Fatalf("error = %v, want save failure", err)
	}
	client.saveCursorFn = nil
	replayed, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 1 || replayed[0] != summaries[0] {
		t.Fatalf("failed delivery did not replay: got %#v, want %#v", replayed, summaries)
	}
	delta, err := client.Get(ReadRequest{Topic: "messages/team", Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 1 {
		t.Fatalf("failed delivery consumed delta: %#v, error = %v", delta, err)
	}
}

// TestContentBroadcastReachesAnUnsubscribedAgentAndSettles covers the one
// delivery an agent never asked for and cannot decline. The reader subscribes
// to nothing, so the collaboration/agents update another agent's join produces
// is the whole of its backlog.
//
// The loop is bounded because the defect it guards is not: a broadcast the
// reader could not read resolved to an empty delta, which covered no summary
// and advanced no cursor, so watch returned the same signal forever. Settling
// is only half the assertion -- the roster line has to actually arrive, since a
// rule that let an empty delta acknowledge its summary would also terminate,
// by discarding the content instead of delivering it.
func TestContentBroadcastReachesAnUnsubscribedAgentAndSettles(t *testing.T) {
	home := t.TempDir()
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	drainAllPending(t, reader)

	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "dev"); err != nil {
		t.Fatal(err)
	}

	delivered := false
	for round := 0; !isQuiescent(t, reader); round++ {
		if round == 10 {
			t.Fatal("broadcast is still pending after 10 acknowledged rounds")
		}
		summaries, err := reader.Watch()
		if err != nil {
			t.Fatal(err)
		}
		batch, err := reader.ResolveBatch(summaries, DeliveryContent)
		if err != nil {
			t.Fatal(err)
		}
		for _, delivery := range batch.Deliveries {
			if delivery.Delta == nil || delivery.Delta.Topic != collaborationAgentsTopic {
				continue
			}
			for _, publication := range delivery.Delta.Publications {
				for _, record := range publication.Records {
					if record.Text == "writer: dev" {
						delivered = true
					}
				}
			}
		}
		if err := reader.AcknowledgeBatch(batch); err != nil {
			t.Fatal(err)
		}
	}
	if !delivered {
		t.Fatal("the broadcast settled without ever delivering the roster line it announced")
	}
}

// isQuiescent reports whether nothing is pending, by asking for a delivery and
// treating the wait running out as the answer.
func isQuiescent(t *testing.T, client *Client) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.WatchContext(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return true
		}
		t.Fatal(err)
	}
	return false
}

func TestContentJoinCarriesRoster(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("a", "dev"); err != nil {
		t.Fatal(err)
	}
	// The first signal is the collaboration/agents roster commit itself
	// (an "update", which resolves to a Delta); the join signal that
	// carries the roster follows it.
	drainSummaries(t, client, 1)
	summary := watchOne(t, client)
	delivery, err := client.ResolveDelivery(summary, DeliveryContent)
	if err != nil || len(delivery.Roster) != 1 || delivery.Roster[0].Name != "a" {
		t.Fatalf("delivery = %#v, error = %v", delivery, err)
	}
}

func TestResolveBatchGroupsSameTopicSignalsAndCapsAtDefaultReadLimit(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	drainAllPending(t, reader)

	// One Put per publication, so this backlog spans more atomic publications
	// than the default cap -- all on the same topic, so they group into one
	// resolved delivery instead of one Get call apiece (which would each
	// redundantly re-fetch the same still-unacknowledged window).
	for i := 0; i < DefaultReadLimit+3; i++ {
		if _, err := writer.Put("dev/tasks", "x"); err != nil {
			t.Fatal(err)
		}
	}
	summaries, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != DefaultReadLimit+3 {
		t.Fatalf("pending signals = %d, want %d", len(summaries), DefaultReadLimit+3)
	}

	batch, err := reader.ResolveBatch(summaries, DeliveryContent)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Deliveries) != 1 || batch.Deliveries[0].Delta == nil || len(batch.Deliveries[0].Delta.Publications) != DefaultReadLimit {
		t.Fatalf("batch = %#v", batch)
	}
	if batch.Remaining != 3 || batch.DefaultLimit != DefaultReadLimit {
		t.Fatalf("batch remaining/default_read_limit = %d/%d, want 3/%d", batch.Remaining, batch.DefaultLimit, DefaultReadLimit)
	}

	cursorWrites := 0
	reader.saveCursorFn = func(agent string, cursor Cursor) error {
		cursorWrites++
		return writeJSONAtomic(filepath.Join(reader.Home, "cursors", agent+".json"), cursor)
	}
	if err := reader.AcknowledgeBatch(batch); err != nil {
		t.Fatal(err)
	}
	if cursorWrites != 1 {
		t.Fatalf("batch cursor writes = %d, want 1", cursorWrites)
	}
	leftover, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(leftover) != 3 {
		t.Fatalf("leftover signals after acknowledging the batch = %d, want 3", len(leftover))
	}
}

func TestResolveBatchGroupsJoinAndLeaveSignalsAcrossTopics(t *testing.T) {
	home := t.TempDir()
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("collaboration/agents"); err != nil {
		t.Fatal(err)
	}
	drainAllPending(t, reader)

	// Three more agents join, each producing an "update" (collaboration/agents)
	// and a "join" (registry) signal -- six pending signals from four agents.
	for _, name := range []string{"a", "b", "c"} {
		other := newTestClient(t, home, "")
		if _, err := other.Join(name, "dev"); err != nil {
			t.Fatal(err)
		}
	}
	summaries, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 6 {
		t.Fatalf("pending signals = %d, want 6", len(summaries))
	}

	batch, err := reader.ResolveBatch(summaries, DeliveryContent)
	if err != nil {
		t.Fatal(err)
	}
	// The three "update" signals share one collaboration/agents delivery, and
	// the three "join" signals share one roster delivery -- two total, not six.
	if len(batch.Deliveries) != 2 {
		t.Fatalf("batch deliveries = %#v, want 2 (one update group, one membership group)", batch.Deliveries)
	}
	var sawUpdate, sawJoin bool
	for _, delivery := range batch.Deliveries {
		switch delivery.Summary.Type {
		case "update":
			sawUpdate = true
		case "join":
			sawJoin = true
			if len(delivery.Roster) != 4 {
				t.Fatalf("join delivery roster = %#v, want 4 agents", delivery.Roster)
			}
		}
	}
	if !sawUpdate || !sawJoin {
		t.Fatalf("batch = %#v, want one update delivery and one join delivery", batch)
	}

	if err := reader.AcknowledgeBatch(batch); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := reader.WatchContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected no pending signals after acknowledging the batch, got err=%v", err)
	}
}

func TestFailedGroupedBatchAcknowledgmentReplaysSummariesAndContent(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	drainAllPending(t, reader)
	for range 2 {
		if _, err := writer.Put("dev/tasks", "x"); err != nil {
			t.Fatal(err)
		}
	}
	summaries, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	batch, err := reader.ResolveBatch(summaries, DeliveryContent)
	if err != nil {
		t.Fatal(err)
	}
	saveFailure := errors.New("save failed")
	reader.saveCursorFn = func(string, Cursor) error {
		return saveFailure
	}
	if err := reader.AcknowledgeBatch(batch); !errors.Is(err, saveFailure) {
		t.Fatalf("error = %v, want save failure", err)
	}
	reader.saveCursorFn = nil

	replayed, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, summaries) {
		t.Fatalf("replayed summaries = %#v, want %#v", replayed, summaries)
	}
	delta, err := reader.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 2 {
		t.Fatalf("replayed delta = %#v, error = %v", delta, err)
	}
}

func TestBatchDoesNotAcknowledgeSummaryReportedAsRemaining(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	drainAllPending(t, reader)
	if _, err := writer.Put("dev/tasks", "x"); err != nil {
		t.Fatal(err)
	}
	summaries, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	delta, err := reader.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.AcknowledgeRead(delta); err != nil {
		t.Fatal(err)
	}

	batch, err := reader.ResolveBatch(summaries, DeliveryContent)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Remaining != len(summaries) {
		t.Fatalf("remaining = %d, want %d", batch.Remaining, len(summaries))
	}
	if err := reader.AcknowledgeBatch(batch); err != nil {
		t.Fatal(err)
	}
	replayed, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, summaries) {
		t.Fatalf("remaining summaries were acknowledged: got %#v, want %#v", replayed, summaries)
	}
}

func TestBatchPersistsTopicOnlyProgressWithoutAcknowledgingRemainingSummary(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	drainAllPending(t, reader)

	if err := writer.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	for range DefaultReadLimit + 1 {
		if _, err := writer.StagePut("old"); err != nil {
			t.Fatal(err)
		}
	}
	oldPublication, err := writer.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Put("dev/tasks", "new"); err != nil {
		t.Fatal(err)
	}
	all, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("summaries = %#v, want two", all)
	}
	if err := reader.AcknowledgeSummary(all[0]); err != nil {
		t.Fatal(err)
	}
	pending, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != all[1] {
		t.Fatalf("pending summaries = %#v, want %#v", pending, all[1:])
	}

	firstBatch, err := reader.ResolveBatch(pending, DeliveryContent)
	if err != nil {
		t.Fatal(err)
	}
	if firstBatch.Remaining != 1 || len(firstBatch.Deliveries) != 1 || len(firstBatch.Deliveries[0].coveredSummaries) != 0 {
		t.Fatalf("first batch = %#v", firstBatch)
	}
	if err := reader.AcknowledgeBatch(firstBatch); err != nil {
		t.Fatal(err)
	}
	cursor, err := reader.loadCursor("reader")
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Topics["dev/tasks"] != oldPublication.Sequence {
		t.Fatalf("topic cursor = %d, want %d", cursor.Topics["dev/tasks"], oldPublication.Sequence)
	}
	replayed, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 1 || replayed[0] != pending[0] {
		t.Fatalf("remaining summary was acknowledged: %#v", replayed)
	}
	secondBatch, err := reader.ResolveBatch(replayed, DeliveryContent)
	if err != nil {
		t.Fatal(err)
	}
	if secondBatch.Remaining != 0 || len(secondBatch.Deliveries) != 1 || secondBatch.Deliveries[0].Delta == nil || len(secondBatch.Deliveries[0].Delta.Publications) != 1 {
		t.Fatalf("second batch = %#v", secondBatch)
	}
}

func TestAcknowledgeBatchSupportsDeliveryWithoutCoverageMetadata(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	summaries, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) == 0 {
		t.Fatal("watch returned no summaries")
	}
	summary := summaries[0]

	batch := BatchDelivery{Deliveries: []Delivery{{Summary: summary, Mode: DeliverySummary}}}
	if err := client.AcknowledgeBatch(batch); err != nil {
		t.Fatal(err)
	}
	cursor, err := client.loadCursor("reader")
	if err != nil {
		t.Fatal(err)
	}
	if !indexAcknowledged(cursor.SummaryRanges, summary.Sequence) {
		t.Fatalf("summary was not acknowledged: %#v", cursor)
	}
}

func TestResolveBatchSpendsSharedBudgetAcrossDifferentTopics(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	for _, topic := range []string{"dev/tasks", "dev/notes"} {
		if _, err := reader.SubscribeTopic(topic); err != nil {
			t.Fatal(err)
		}
	}
	drainAllPending(t, reader)

	// A first topic just under the cap, then a second topic that only
	// partially fits in what's left of the shared budget.
	for i := 0; i < DefaultReadLimit-5; i++ {
		if _, err := writer.Put("dev/tasks", "x"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		if _, err := writer.Put("dev/notes", "y"); err != nil {
			t.Fatal(err)
		}
	}
	summaries, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != DefaultReadLimit-5+10 {
		t.Fatalf("pending signals = %d, want %d", len(summaries), DefaultReadLimit-5+10)
	}

	batch, err := reader.ResolveBatch(summaries, DeliveryContent)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Deliveries) != 2 {
		t.Fatalf("batch deliveries = %#v, want one per topic", batch.Deliveries)
	}
	first, second := batch.Deliveries[0], batch.Deliveries[1]
	if first.Summary.Topic != "dev/tasks" || len(first.Delta.Publications) != DefaultReadLimit-5 {
		t.Fatalf("first delivery = %#v", first)
	}
	if second.Summary.Topic != "dev/notes" || len(second.Delta.Publications) != 5 {
		t.Fatalf("second delivery = %#v, want exactly enough to spend the remaining budget of 5", second)
	}
	if batch.Remaining != 5 || batch.DefaultLimit != DefaultReadLimit {
		t.Fatalf("batch remaining/default_read_limit = %d/%d, want 5/%d", batch.Remaining, batch.DefaultLimit, DefaultReadLimit)
	}
}

func TestResolveBatchAlwaysIncludesFirstDeliveryWhole(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	drainAllPending(t, reader)

	// One oversized publication (more records than the cap on its own),
	// followed by one ordinary one.
	if err := writer.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < DefaultReadLimit+2; i++ {
		if _, err := writer.StagePut("x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Put("dev/tasks", "y"); err != nil {
		t.Fatal(err)
	}

	summaries, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("pending signals = %d, want 2", len(summaries))
	}
	batch, err := reader.ResolveBatch(summaries, DeliveryContent)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Deliveries) != 1 || len(batch.Deliveries[0].Delta.Publications[0].Records) != DefaultReadLimit+2 {
		t.Fatalf("batch = %#v", batch)
	}
	if batch.Remaining != 1 || batch.DefaultLimit != DefaultReadLimit {
		t.Fatalf("batch remaining/default_read_limit = %d/%d, want 1/%d", batch.Remaining, batch.DefaultLimit, DefaultReadLimit)
	}
}

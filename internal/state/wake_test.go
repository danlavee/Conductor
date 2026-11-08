package state

import (
	"strings"
	"testing"
	"time"
)

// Publish admits only what one wake can show whole: at most WakeRecords
// records, rendering within the budget. Text is counted in UTF-16 units, so a
// record of 2,000 astral runes takes 4,000 of it.
func TestPublishAdmitsOnlyWhatOneWakeShowsWhole(t *testing.T) {
	writer := joined(t, t.TempDir(), "writer")
	astral := strings.Repeat("😀", MaxRecordText)
	for _, admitted := range [][]Op{creates(WakeRecords, "x"), creates(2, astral)} {
		if _, err := writer.Publish("work/fits", admitted); err != nil {
			t.Fatalf("publication of %d records was refused: %v", len(admitted), err)
		}
	}
	for name, refused := range map[string][]Op{
		"one record too many":       creates(WakeRecords+1, "x"),
		"five records too long":     creates(WakeRecords, strings.Repeat("x", MaxRecordText)),
		"three astral records long": creates(3, astral),
	} {
		_, err := writer.Publish("work/refused", refused)
		assertCode(t, err, "INVALID")
		if name == "one record too many" && !strings.Contains(err.Error(), "at most 5") {
			t.Fatalf("%s: error = %v, want it to name the record cap", name, err)
		}
		if name != "one record too many" && !strings.Contains(err.Error(), "renders to") {
			t.Fatalf("%s: error = %v, want it to name the wake length", name, err)
		}
	}
}

// A wake shows whole publications while they fit, counts the rest in its
// footer, and stays within WakeTextLimit footer included. A delivery shown in
// part acknowledges only through its last shown publication.
func TestRenderWakeShowsWholePublicationsAndAcksOnlyThose(t *testing.T) {
	long := strings.Repeat("😀", MaxRecordText) // 4,000 units each
	publication := func(sequence int64, records int, text string) Publication {
		p := Publication{Sequence: sequence, Topic: "work/a", Agent: "writer", Timestamp: time.Unix(0, 0)}
		for i := 0; i < records; i++ {
			p.Records = append(p.Records, Record{Index: sequence*10 + int64(i), Text: text})
		}
		return p
	}
	batch := Batch{Remaining: 7, Deliveries: []ReadResult{
		{Topic: "work/a", maxSequence: 9, Publications: []Publication{publication(1, 1, long), publication(2, 1, long), publication(3, 1, long)}},
		{Topic: "work/b", maxSequence: 6, Publications: []Publication{publication(4, 1, "small")}},
	}}
	text, shown := RenderWake(batch)
	if wakeLength(text) > WakeTextLimit {
		t.Fatalf("wake is %d units, over %d", wakeLength(text), WakeTextLimit)
	}
	if !strings.HasPrefix(text, "Conductor: 2 new records for you.\n\n") || !strings.HasSuffix(text, "(9 more unread; they follow on their own.)\n") {
		t.Fatalf("wake framing = %q ... %q", text[:40], text[len(text)-50:])
	}
	if len(shown) != 1 || len(shown[0].Publications) != 2 || shown[0].maxSequence != 2 {
		t.Fatalf("shown = %d deliveries; want work/a cut to its first two publications, acked through 2", len(shown))
	}

	// Shown whole, a delivery keeps its own progress, which may pass over the
	// reader's own publications.
	text, shown = RenderWake(Batch{Deliveries: batch.Deliveries[1:]})
	if strings.Contains(text, "more unread") || len(shown) != 1 || shown[0].maxSequence != 6 {
		t.Fatalf("whole delivery: %q, %#v", text, shown)
	}
}

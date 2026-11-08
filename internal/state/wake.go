package state

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf16"
)

// A wake is one plain-text message, the same on every host and from watch.
// WakeTextLimit is the lowest host envelope proven to pass whole: Grok's
// reason and additionalContext keep 10,000 runes, Claude's additionalContext
// passes 10,000 UTF-16 units whole. Counting UTF-16 units fits both.
const (
	WakeRecords   = 5
	WakeTextLimit = 10000
)

// wakeBudget is what a wake's publications may take: the limit less the
// widest preamble and footer any wake can carry. Publish admits only a
// publication that fits it alone, so every wake can show its first.
var wakeBudget = WakeTextLimit - wakeLength(wakePreamble(WakeRecords)) - wakeLength(wakeFooter(math.MaxInt))

// RenderWake writes batch as one wake: whole publications in order, while
// they fit WakeRecords and the text budget. It returns the deliveries shown,
// each cut to the publications it showed; only those may be acknowledged.
func RenderWake(batch Batch) (text string, shown []ReadResult) {
	var body strings.Builder
	records, length, unshown, full := 0, 0, batch.Remaining, false
	for _, delivery := range batch.Deliveries {
		cut := delivery
		cut.Publications = nil
		for _, publication := range delivery.Publications {
			rendered := renderPublication(publication)
			if full || records+len(publication.Records) > WakeRecords || length+wakeLength(rendered) > wakeBudget {
				full = true
				unshown += len(publication.Records)
				continue
			}
			body.WriteString(rendered)
			records += len(publication.Records)
			length += wakeLength(rendered)
			cut.Publications = append(cut.Publications, publication)
			cut.maxSequence = publication.Sequence
		}
		if len(cut.Publications) == len(delivery.Publications) {
			cut.maxSequence = delivery.maxSequence // past the reader's own, too
		}
		if len(cut.Publications) > 0 {
			shown = append(shown, cut)
		}
	}
	text = wakePreamble(records) + body.String()
	if unshown > 0 {
		text += wakeFooter(unshown)
	}
	return text, shown
}

// admitWake rejects a publication that could not be shown whole in one wake.
func admitWake(publication Publication) error {
	if count := len(publication.Records); count > WakeRecords {
		return &ProtocolError{Code: "INVALID", Text: fmt.Sprintf("the publication has %d records; one holds at most %d, so publish the rest separately", count, WakeRecords)}
	}
	if length := wakeLength(renderPublication(publication)); length > wakeBudget {
		return &ProtocolError{Code: "INVALID", Text: fmt.Sprintf("the publication renders to %d characters; one wake shows at most %d, so publish fewer or shorter records together", length, wakeBudget)}
	}
	return nil
}

func renderPublication(publication Publication) string {
	var text strings.Builder
	for _, record := range publication.Records {
		fmt.Fprintf(&text, "[%s #%d] from %s, %s\n%s\n\n", publication.Topic, record.Index, publication.Agent,
			publication.Timestamp.UTC().Format("2006-01-02 15:04Z"), record.Text)
	}
	return text.String()
}

func wakePreamble(records int) string {
	if records == 1 {
		return "Conductor: 1 new record for you.\n\n"
	}
	return fmt.Sprintf("Conductor: %d new records for you.\n\n", records)
}

func wakeFooter(unshown int) string {
	return fmt.Sprintf("(%d more unread; they follow on their own.)\n", unshown)
}

// wakeLength counts UTF-16 code units, a JavaScript host's string length.
func wakeLength(text string) int { return len(utf16.Encode([]rune(text))) }

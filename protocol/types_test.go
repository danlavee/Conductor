package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/danlavee/Conductor/protocol"
)

func TestRecordWireShapeIsOnlyIndexAndText(t *testing.T) {
	encoded, err := json.Marshal(protocol.Record{Index: 7, Text: "plain text"})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"index":7,"text":"plain text"}` {
		t.Fatalf("record JSON = %s", encoded)
	}
}

func TestSummaryWireShape(t *testing.T) {
	encoded, err := json.Marshal(protocol.Summary{Type: "update", Topic: "dev/tasks", Sequence: 4, Agent: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"update","topic":"dev/tasks","sequence":4,"agent":"writer"}`
	if string(encoded) != want {
		t.Fatalf("summary JSON = %s", encoded)
	}
}

package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/danlavee/Conductor/protocol"
)

func TestMessageMutationWireShape(t *testing.T) {
	payload := protocol.MessagePayload{Text: "hello"}
	encoded, err := json.Marshal(protocol.MessageMutation{Operation: protocol.MutationSet, Kind: "team-defined kind / 任意", Payload: &payload})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"operation":"set","kind":"team-defined kind / 任意","payload":{"text":"hello"}}`
	if string(encoded) != want {
		t.Fatalf("mutation JSON = %s, want %s", encoded, want)
	}
}

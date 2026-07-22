package conductor_test

import (
	"testing"

	conductor "github.com/danlavee/Conductor"
)

func TestPublicFacadePublishesProtocolMessages(t *testing.T) {
	client, err := conductor.New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register("writer", "messages"); err != nil {
		t.Fatal(err)
	}
	payload := conductor.MessagePayload{Text: "hello"}
	publication, err := client.Put("messages/team", map[string]conductor.MessageMutation{
		"entry": {Operation: conductor.MutationSet, Kind: "any kind", Payload: &payload},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Get(conductor.ReadRequest{Resource: "messages/team", Key: "entry", Mode: conductor.ReadFull})
	if err != nil || len(result.Messages) != 1 || result.Messages[0].Index != publication.Index || result.Messages[0].Payload.Text != "hello" {
		t.Fatalf("public read = %#v, %v", result, err)
	}
}

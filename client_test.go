package conductor_test

import (
	"testing"

	conductor "github.com/danlavee/Conductor"
)

func TestPublicFacadeCreatesAndEditsRecords(t *testing.T) {
	client, err := conductor.New(t.TempDir(), "writer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	dispatch, err := client.Put("messages/team", "hello")
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err = client.Edit("messages/team", dispatch.Record().Index, "updated")
	if err != nil || dispatch.Record().Text != "updated" {
		t.Fatalf("dispatch = %#v, error = %v", dispatch, err)
	}
	// The publisher is the only agent here and it is subscribed to nothing, so
	// the round trip also pins the answer a publication exists to give: a
	// record that reached nobody says so.
	if len(dispatch.Recipients) != 0 {
		t.Fatalf("recipients = %#v; want none for a topic nobody subscribes to", dispatch.Recipients)
	}
	record := dispatch.Record()
	result, err := client.Get(conductor.ReadRequest{Topic: "messages/team", RecordIndex: record.Index, Mode: conductor.ReadFull})
	if err != nil || len(result.Records) != 1 || result.Records[0] != record {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

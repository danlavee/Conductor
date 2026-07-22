package conductor_test

import (
	"testing"

	conductor "github.com/danlavee/Conductor"
)

func TestPublicFacadeCreatesEditsAndStrikesRecords(t *testing.T) {
	client, err := conductor.New(t.TempDir(), "writer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register("writer", "records"); err != nil {
		t.Fatal(err)
	}
	record, err := client.Put("messages/team", "hello")
	if err != nil {
		t.Fatal(err)
	}
	record, err = client.Edit("messages/team", record.Index, "updated")
	if err != nil {
		t.Fatal(err)
	}
	record, err = client.Strike("messages/team", record.Index)
	if err != nil || record.Text != "~~updated~~" {
		t.Fatalf("record = %#v, error = %v", record, err)
	}
	result, err := client.Get(conductor.ReadRequest{Topic: "messages/team", RecordIndex: record.Index, Mode: conductor.ReadFull})
	if err != nil || len(result.Records) != 1 || result.Records[0] != record {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

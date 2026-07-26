package codexdesktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	conductor "github.com/danlavee/Conductor"
)

type stubWatchClient struct {
	summaries    []conductor.Summary
	watchErr     error
	watchCalls   int
	sawCancelled bool
	mode         conductor.DeliveryMode
	batch        conductor.BatchDelivery
	acknowledged bool
}

func (c *stubWatchClient) WatchContext(ctx context.Context) ([]conductor.Summary, error) {
	c.watchCalls++
	c.sawCancelled = errors.Is(ctx.Err(), context.Canceled)
	if c.watchErr != nil {
		return nil, c.watchErr
	}
	if len(c.summaries) == 0 {
		return nil, ctx.Err()
	}
	return c.summaries, nil
}

func (c *stubWatchClient) ResolveBatch(_ []conductor.Summary, mode conductor.DeliveryMode) (conductor.BatchDelivery, error) {
	c.mode = mode
	return c.batch, nil
}

func (c *stubWatchClient) AcknowledgeBatch(conductor.BatchDelivery) error {
	c.acknowledged = true
	return nil
}

func TestRunReturnsIdleWithoutAcknowledging(t *testing.T) {
	client := &stubWatchClient{}
	var output bytes.Buffer
	if err := Run(context.Background(), client, &output, "a", conductor.DeliveryContent); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if client.watchCalls != 1 || !client.sawCancelled || client.acknowledged || result.Status != StatusIdle || result.Agent != "a" || result.Batch != nil {
		t.Fatalf("client=%#v result=%#v", client, result)
	}
}

func TestRunDeliversActivityAndAcknowledges(t *testing.T) {
	summary := conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 7, Agent: "writer"}
	batch := conductor.BatchDelivery{Deliveries: []conductor.Delivery{{Summary: summary, Mode: conductor.DeliveryContent}}}
	client := &stubWatchClient{summaries: []conductor.Summary{summary}, batch: batch}
	var output bytes.Buffer
	if err := Run(context.Background(), client, &output, "a", conductor.DeliveryContent); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if client.watchCalls != 1 || !client.sawCancelled || !client.acknowledged || client.mode != conductor.DeliveryContent || result.Status != StatusActivity || result.Batch == nil {
		t.Fatalf("client=%#v result=%#v", client, result)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestRunLeavesActivityUnreadWhenOutputFails(t *testing.T) {
	summary := conductor.Summary{Type: "update", Topic: "dev/tasks", Sequence: 7, Agent: "writer"}
	client := &stubWatchClient{
		summaries: []conductor.Summary{summary},
		batch:     conductor.BatchDelivery{Deliveries: []conductor.Delivery{{Summary: summary}}},
	}
	if err := Run(context.Background(), client, failingWriter{}, "a", conductor.DeliveryContent); err == nil {
		t.Fatal("expected output failure")
	}
	if client.acknowledged {
		t.Fatal("activity was acknowledged after output failed")
	}
}

func TestRunHonorsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &stubWatchClient{}
	if err := Run(ctx, client, &bytes.Buffer{}, "a", conductor.DeliveryContent); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if client.watchCalls != 0 {
		t.Fatal("watch started after caller cancellation")
	}
}

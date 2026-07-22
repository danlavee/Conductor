package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	conductor "github.com/danlavee/Conductor"
)

func TestActivateWaitsForMatchingCompletedTurn(t *testing.T) {
	serverInput, clientOutput := io.Pipe()
	clientInput, serverOutput := io.Pipe()
	session := &Session{
		input:   clientOutput,
		events:  make(chan message, 8),
		readErr: make(chan error, 1),
		sandbox: "danger-full-access",
	}
	go session.read(clientInput)
	serverDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverInput)
		if !scanner.Scan() {
			serverDone <- scanner.Err()
			return
		}
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			serverDone <- err
			return
		}
		if request.Method != "turn/start" {
			serverDone <- &testError{"method = " + request.Method}
			return
		}
		encoder := json.NewEncoder(serverOutput)
		if err := encoder.Encode(map[string]any{"id": request.ID, "result": map[string]any{"turn": map[string]any{"id": "turn-1"}}}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}})
	}()

	err := session.Activate(context.Background(), "thread-1", "tester1", conductor.Delivery{Summary: conductor.Summary{Sequence: 3, Type: "update", Topic: "dev/tasks", Agent: "tester1"}, Mode: conductor.DeliveryContent})
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	_ = clientOutput.Close()
	_ = serverOutput.Close()
}

func TestSandboxPolicy(t *testing.T) {
	policy, err := sandboxPolicy("danger-full-access")
	if err != nil || policy["type"] != "dangerFullAccess" {
		t.Fatalf("danger-full-access = %#v, %v", policy, err)
	}
	if _, err := sandboxPolicy("unknown"); err == nil || !strings.Contains(err.Error(), "must be") {
		t.Fatalf("unknown sandbox error = %v", err)
	}
}

type testError struct{ message string }

func (e *testError) Error() string { return e.message }

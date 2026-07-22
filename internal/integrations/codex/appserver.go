package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	conductor "github.com/danlavee/Conductor"
)

const AppServerDeliveryEnvironment = "CONDUCTOR_CODEX_APP_SERVER_DELIVERY"

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Session struct {
	command *exec.Cmd
	input   io.WriteCloser
	events  chan message
	readErr chan error
	output  io.Writer
	sandbox string
	pending []message
	nextID  int64
	mu      sync.Mutex
}

func Start(ctx context.Context, executable, threadID, sandbox string, output, stderr io.Writer) (*Session, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, fmt.Errorf("%s is required for Codex watch", ThreadEnvironment)
	}
	var err error
	executable, err = resolveExecutable(executable)
	if err != nil {
		return nil, err
	}
	if _, err := sandboxPolicy(sandbox); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, executable, "app-server", "--listen", "stdio://")
	command.Env = append(os.Environ(), AppServerDeliveryEnvironment+"=1", ThreadEnvironment+"="+threadID)
	command.Stderr = stderr
	input, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex app-server input: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex app-server output: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}
	session := &Session{command: command, input: input, events: make(chan message, 32), readErr: make(chan error, 1), output: output, sandbox: sandbox}
	go session.read(stdout)
	if _, err := session.request(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "conductor", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": false},
	}); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("initialize Codex app-server: %w", err)
	}
	if err := session.notify("initialized", map[string]any{}); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("initialize Codex app-server: %w", err)
	}
	resume := map[string]any{"threadId": threadID, "approvalPolicy": "never"}
	if strings.TrimSpace(sandbox) != "" {
		resume["sandbox"] = strings.TrimPrefix(strings.TrimSpace(sandbox), ":")
	}
	if _, err := session.request(ctx, "thread/resume", resume); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("resume Codex app-server thread %s: %w", threadID, err)
	}
	return session, nil
}

func (s *Session) Activate(ctx context.Context, threadID, agent string, signal conductor.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prompt, err := SignalPrompt(agent, signal)
	if err != nil {
		return err
	}
	params := map[string]any{
		"threadId":       threadID,
		"approvalPolicy": "never",
		"input":          []map[string]string{{"type": "text", "text": prompt}},
	}
	if policy, err := sandboxPolicy(s.sandbox); err != nil {
		return err
	} else if policy != nil {
		params["sandboxPolicy"] = policy
	}
	result, err := s.request(ctx, "turn/start", params)
	if err != nil {
		return fmt.Errorf("start Codex app-server turn: %w", err)
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(result, &started); err != nil || started.Turn.ID == "" {
		return errors.New("Codex app-server returned an invalid turn/start response")
	}
	for {
		event, err := s.next(ctx)
		if err != nil {
			return err
		}
		if len(event.ID) > 0 && event.Method != "" {
			_ = s.respondError(event.ID, -32601, "Conductor does not support interactive app-server requests")
			continue
		}
		if event.Method != "turn/completed" {
			continue
		}
		var completed struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		if err := json.Unmarshal(event.Params, &completed); err != nil || completed.ThreadID != threadID || completed.Turn.ID != started.Turn.ID {
			continue
		}
		if completed.Turn.Status != "completed" {
			if completed.Turn.Error != nil && completed.Turn.Error.Message != "" {
				return fmt.Errorf("Codex app-server turn %s ended %s: %s", completed.Turn.ID, completed.Turn.Status, completed.Turn.Error.Message)
			}
			return fmt.Errorf("Codex app-server turn %s ended %s", completed.Turn.ID, completed.Turn.Status)
		}
		return nil
	}
}

func (s *Session) Close() error {
	if s.input != nil {
		_ = s.input.Close()
	}
	if s.command == nil || s.command.Process == nil {
		return nil
	}
	if err := s.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	_ = s.command.Wait()
	return nil
}

func (s *Session) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.nextID++
	id := s.nextID
	if err := s.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		event, err := s.receive(ctx)
		if err != nil {
			return nil, err
		}
		if len(event.ID) == 0 {
			s.pending = append(s.pending, event)
			continue
		}
		var responseID int64
		if err := json.Unmarshal(event.ID, &responseID); err == nil && responseID == id && event.Method == "" {
			if event.Error != nil {
				return nil, fmt.Errorf("RPC %d: %s", event.Error.Code, event.Error.Message)
			}
			return event.Result, nil
		}
		if event.Method != "" {
			_ = s.respondError(event.ID, -32601, "Conductor does not support interactive app-server requests")
		}
	}
}

func (s *Session) notify(method string, params any) error {
	return s.write(map[string]any{"method": method, "params": params})
}

func (s *Session) respondError(id json.RawMessage, code int, message string) error {
	return s.write(map[string]any{"id": id, "error": rpcError{Code: code, Message: message}})
}

func (s *Session) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = s.input.Write(data)
	return err
}

func (s *Session) read(input io.Reader) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var event message
		if err := json.Unmarshal(line, &event); err != nil {
			s.readErr <- fmt.Errorf("decode Codex app-server message: %w", err)
			return
		}
		if s.output != nil && (event.Method == "turn/completed" || event.Method == "error") {
			_, _ = s.output.Write(append(append([]byte(nil), line...), '\n'))
		}
		s.events <- event
	}
	s.readErr <- scanner.Err()
}

func (s *Session) next(ctx context.Context) (message, error) {
	if len(s.pending) > 0 {
		event := s.pending[0]
		s.pending = s.pending[1:]
		return event, nil
	}
	return s.receive(ctx)
}

func (s *Session) receive(ctx context.Context) (message, error) {
	select {
	case <-ctx.Done():
		return message{}, ctx.Err()
	case event := <-s.events:
		return event, nil
	case err := <-s.readErr:
		if err == nil {
			return message{}, errors.New("Codex app-server closed its output")
		}
		return message{}, err
	}
}

func sandboxPolicy(value string) (map[string]any, error) {
	switch strings.TrimPrefix(strings.TrimSpace(value), ":") {
	case "":
		return nil, nil
	case "read-only":
		return map[string]any{"type": "readOnly", "networkAccess": false}, nil
	case "workspace-write":
		return map[string]any{"type": "workspaceWrite", "networkAccess": false, "writableRoots": []string{}}, nil
	case "danger-full-access":
		return map[string]any{"type": "dangerFullAccess"}, nil
	default:
		return nil, fmt.Errorf("%s must be read-only, workspace-write, or danger-full-access", SandboxEnvironment)
	}
}

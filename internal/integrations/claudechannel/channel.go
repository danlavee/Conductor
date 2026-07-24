// Package claudechannel exposes Conductor as a Claude Code MCP channel.
package claudechannel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	conductor "github.com/danlavee/Conductor"
)

const protocolVersion = "2025-11-25"

type WatchClient interface {
	WatchContext(context.Context) ([]conductor.Summary, error)
	AcknowledgeSummary(conductor.Summary) error
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type outboundNotification struct {
	JSONRPC string              `json:"jsonrpc"`
	Method  string              `json:"method"`
	Params  channelNotification `json:"params"`
}

type channelNotification struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type synchronizedEncoder struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

func Run(ctx context.Context, client WatchClient, input io.Reader, output io.Writer, agent string) error {
	if strings.TrimSpace(agent) == "" {
		return errors.New("Claude channel requires an agent name")
	}
	if input == nil || output == nil {
		return errors.New("Claude channel requires MCP input and output")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	writer := &synchronizedEncoder{encoder: json.NewEncoder(output)}
	inbound := make(chan message)
	readErrors := make(chan error, 1)
	go readMessages(input, inbound, readErrors)

	initialized := false
	watchErrors := make(chan error, 1)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErrors:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case err := <-watchErrors:
			return err
		case incoming := <-inbound:
			switch incoming.Method {
			case "initialize":
				if initialized {
					if err := writer.write(responseForError(incoming.ID, -32600, "Claude channel is already initialized")); err != nil {
						return err
					}
					continue
				}
				var params initializeParams
				if err := json.Unmarshal(incoming.Params, &params); err != nil || params.ProtocolVersion == "" {
					if err := writer.write(responseForError(incoming.ID, -32602, "initialize requires protocolVersion")); err != nil {
						return err
					}
					continue
				}
				result := map[string]any{
					"protocolVersion": negotiatedVersion(params.ProtocolVersion),
					"capabilities": map[string]any{
						"experimental": map[string]any{"claude/channel": map[string]any{}},
					},
					"serverInfo": map[string]string{
						"name":    "conductor",
						"version": "1",
					},
					"instructions": "Conductor events identify shared state that changed. Process each event with the installed Conductor skill; read the named resource for updates and refresh the roster for joins or leaves.",
				}
				if err := writer.write(response{JSONRPC: "2.0", ID: incoming.ID, Result: result}); err != nil {
					return err
				}
			case "notifications/initialized":
				if initialized {
					continue
				}
				initialized = true
				go watch(ctx, client, writer, agent, watchErrors)
			case "ping":
				if err := writer.write(response{JSONRPC: "2.0", ID: incoming.ID, Result: map[string]any{}}); err != nil {
					return err
				}
			default:
				if len(incoming.ID) == 0 {
					continue
				}
				if err := writer.write(responseForError(incoming.ID, -32601, "method not found")); err != nil {
					return err
				}
			}
		}
	}
}

func watch(ctx context.Context, client WatchClient, writer *synchronizedEncoder, agent string, result chan<- error) {
	for {
		summaries, err := client.WatchContext(ctx)
		if err != nil {
			result <- err
			return
		}
		for _, summary := range summaries {
			notification := outboundNotification{
				JSONRPC: "2.0",
				Method:  "notifications/claude/channel",
				Params:  summaryNotification(agent, summary),
			}
			if err := writer.write(notification); err != nil {
				result <- fmt.Errorf("deliver Conductor summary %d to Claude channel: %w", summary.Sequence, err)
				return
			}
			if err := client.AcknowledgeSummary(summary); err != nil {
				result <- fmt.Errorf("acknowledge Conductor summary %d after Claude channel delivery: %w", summary.Sequence, err)
				return
			}
		}
	}
}

func summaryNotification(agent string, summary conductor.Summary) channelNotification {
	content := fmt.Sprintf("Conductor summary %d (%s) for agent %q. Use the installed Conductor skill now. Read topic %q for an update; refresh the roster for a join or leave. The channel owns the wait loop, so do not start conductor watch. Process idempotently.", summary.Sequence, summary.Type, agent, summary.Topic)
	return channelNotification{
		Content: content,
		Meta: map[string]string{
			"agent":        agent,
			"summary_sequence": strconv.FormatInt(summary.Sequence, 10),
			"summary_type":     summary.Type,
			"topic":            summary.Topic,
			"source_agent":     summary.Agent,
		},
	}
}

func readMessages(input io.Reader, messages chan<- message, result chan<- error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var incoming message
		if err := json.Unmarshal(line, &incoming); err != nil {
			result <- fmt.Errorf("decode Claude MCP message: %w", err)
			return
		}
		if incoming.JSONRPC != "2.0" || incoming.Method == "" {
			result <- errors.New("decode Claude MCP message: invalid JSON-RPC envelope")
			return
		}
		messages <- incoming
	}
	if err := scanner.Err(); err != nil {
		result <- fmt.Errorf("read Claude MCP input: %w", err)
		return
	}
	result <- io.EOF
}

func (writer *synchronizedEncoder) write(value any) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if err := writer.encoder.Encode(value); err != nil {
		return fmt.Errorf("write Claude MCP message: %w", err)
	}
	return nil
}

func responseForError(id json.RawMessage, code int, message string) response {
	return response{JSONRPC: "2.0", ID: id, Error: &responseError{Code: code, Message: message}}
}

func negotiatedVersion(requested string) string {
	if requested == "2025-06-18" || requested == protocolVersion {
		return requested
	}
	return protocolVersion
}

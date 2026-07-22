// Package codex delivers Conductor signals to a local Codex thread through
// either a persistent app-server or a process-per-signal CLI transport.
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

	conductor "github.com/danlavee/Conductor"
)

const (
	BinaryEnvironment      = "CONDUCTOR_CODEX_BIN"
	SandboxEnvironment     = "CONDUCTOR_CODEX_SANDBOX"
	ThreadEnvironment      = "CODEX_THREAD_ID"
	CLIDeliveryEnvironment = "CONDUCTOR_CODEX_DELIVERY"
	PermissionEnvironment  = "CODEX_PERMISSION_PROFILE"
)

type WatchClient interface {
	WatchContext(context.Context) (conductor.Signal, error)
	AcknowledgeSignal(conductor.Signal) error
}

type Activator interface {
	Activate(context.Context, string, string, conductor.Signal) error
}

type CLI struct {
	executable string
	sandbox    string
	stdout     io.Writer
	stderr     io.Writer
}

func New(executable, sandbox string, stdout, stderr io.Writer) (*CLI, error) {
	var err error
	executable, err = resolveExecutable(executable)
	if err != nil {
		return nil, err
	}
	sandbox = strings.TrimPrefix(strings.TrimSpace(sandbox), ":")
	if sandbox != "" && sandbox != "read-only" && sandbox != "workspace-write" && sandbox != "danger-full-access" {
		return nil, fmt.Errorf("%s must be read-only, workspace-write, or danger-full-access", SandboxEnvironment)
	}
	return &CLI{executable: executable, sandbox: sandbox, stdout: stdout, stderr: stderr}, nil
}

func Sandbox(environment map[string]string) string {
	if value := environment[SandboxEnvironment]; value != "" {
		return value
	}
	return environment[PermissionEnvironment]
}

func (a *CLI) Check(ctx context.Context) error {
	command := exec.CommandContext(ctx, a.executable, "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run Codex CLI %q: %w%s", a.executable, err, outputSuffix(output))
	}
	return nil
}

func (a *CLI) Activate(ctx context.Context, threadID, agent string, signal conductor.Signal) error {
	prompt, err := SignalPrompt(agent, signal)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, a.executable, ResumeArguments(threadID, prompt, a.sandbox)...)
	command.Env = setEnvironment(os.Environ(), map[string]string{
		"CONDUCTOR_AGENT":      agent,
		CLIDeliveryEnvironment: "1",
		ThreadEnvironment:      threadID,
	})
	command.Stderr = a.stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("capture Codex output: %w", err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("resume Codex thread %s: %w", threadID, err)
	}
	completed, outputErr := ObserveEvents(stdout, a.stdout)
	waitErr := command.Wait()
	if waitErr != nil {
		return fmt.Errorf("resume Codex thread %s: %w", threadID, waitErr)
	}
	if outputErr != nil {
		return fmt.Errorf("resume Codex thread %s: %w", threadID, outputErr)
	}
	if !completed {
		return fmt.Errorf("resume Codex thread %s: Codex exited without turn.completed", threadID)
	}
	return nil
}

func Run(ctx context.Context, client WatchClient, activator Activator, threadID, agent string) error {
	if strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("%s is required for Codex watch", ThreadEnvironment)
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("Codex watch requires an agent name")
	}
	for {
		signal, err := client.WatchContext(ctx)
		if err != nil {
			return err
		}
		if err := activator.Activate(ctx, threadID, agent, signal); err != nil {
			return fmt.Errorf("deliver Conductor signal %d to Codex: %w", signal.Index, err)
		}
		if err := client.AcknowledgeSignal(signal); err != nil {
			return fmt.Errorf("acknowledge Conductor signal %d after Codex delivery: %w", signal.Index, err)
		}
	}
}

func ResumeArguments(threadID, prompt, sandbox string) []string {
	arguments := []string{"exec", "--skip-git-repo-check", "--json"}
	if sandbox != "" {
		arguments = append(arguments, "--sandbox", sandbox)
	}
	return append(arguments, "resume", threadID, prompt)
}

func SignalPrompt(agent string, signal conductor.Signal) (string, error) {
	payload, err := json.Marshal(signal)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Conductor activated this Codex turn for agent %q with signal %s. Use the installed Conductor skill to process the signal. For an update, read the named resource; for a join or leave, refresh the roster. The adapter already owns the wait loop, so do not start conductor watch. Process this signal idempotently and report the result.", agent, payload), nil
}

func ObserveEvents(input io.Reader, output io.Writer) (bool, error) {
	if output == nil {
		output = io.Discard
	}
	completed := false
	var eventErr error
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		_, _ = output.Write(append(append([]byte(nil), line...), '\n'))
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			if eventErr == nil {
				eventErr = fmt.Errorf("decode Codex JSONL event: %w", err)
			}
			continue
		}
		switch event.Type {
		case "turn.completed":
			completed = true
		case "turn.failed":
			if eventErr == nil {
				eventErr = errors.New("Codex reported turn.failed")
			}
		}
	}
	if err := scanner.Err(); err != nil && eventErr == nil {
		eventErr = fmt.Errorf("read Codex JSONL events: %w", err)
	}
	return completed, eventErr
}

func outputSuffix(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return ""
	}
	return ": " + trimmed
}

func setEnvironment(environment []string, values map[string]string) []string {
	result := make([]string, 0, len(environment)+len(values))
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replace := values[strings.ToUpper(key)]; replace {
				continue
			}
		}
		result = append(result, entry)
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

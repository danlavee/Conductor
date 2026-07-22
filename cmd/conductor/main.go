package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/install"
	skillbundle "github.com/danlavee/Conductor/skills"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		var protocol *conductor.ProtocolError
		if errors.As(err, &protocol) {
			_ = conductor.WriteJSON(os.Stderr, protocol)
		} else {
			_ = conductor.WriteJSON(os.Stderr, map[string]string{"code": "INVALID", "message": err.Error()})
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "install":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			return installUsageError()
		}
		if err := install.ValidateDestination(args[1]); err != nil {
			return installUsageError()
		}
		executablePath, err := os.Executable()
		if err != nil {
			return err
		}
		result, err := install.Install(args[1], install.Source{
			Bundle:         skillbundle.Files,
			ExecutablePath: executablePath,
			Version:        currentVersion(),
			Protocol:       conductor.CurrentProtocolVersion,
			GOOS:           runtime.GOOS,
			GOARCH:         runtime.GOARCH,
			SmokeCheck:     true,
		})
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, result)
	case "version":
		if len(args) != 1 {
			return errors.New("usage: conductor version")
		}
		return conductor.WriteJSON(os.Stdout, struct {
			Version  string `json:"version"`
			Protocol int    `json:"protocol"`
		}{Version: currentVersion(), Protocol: conductor.CurrentProtocolVersion})
	}
	client, err := conductor.New(os.Getenv("CONDUCTOR_HOME"), os.Getenv("CONDUCTOR_AGENT"))
	if err != nil {
		return err
	}
	switch args[0] {
	case "register":
		if len(args) != 3 {
			return usageError()
		}
		snapshot, err := client.Register(args[1], args[2])
		if err != nil {
			return err
		}
		if err := conductor.WriteJSON(os.Stdout, snapshot); err != nil {
			return err
		}
		return client.AcknowledgeSnapshot(snapshot)
	case "deregister":
		if len(args) != 2 {
			return usageError()
		}
		client.Agent = args[1]
		if err := client.Deregister(args[1]); err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, map[string]string{"deregistered": args[1]})
	case "list-agents":
		if len(args) != 1 {
			return usageError()
		}
		agents, err := client.ListAgents()
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, agents)
	case "begin":
		resource, options, err := parseBegin(args[1:])
		if err != nil {
			return err
		}
		if err := client.BeginWithOptions(resource, options); err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, map[string]string{"status": "begun", "resource": resource})
	case "set":
		if len(args) != 4 {
			return usageError()
		}
		if err := client.Set(args[1], args[2], args[3]); err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, map[string]string{"status": "buffered", "key": args[1]})
	case "unset":
		if len(args) != 2 {
			return usageError()
		}
		if err := client.Scratch(args[1]); err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, map[string]string{"status": "buffered", "key": args[1]})
	case "commit":
		if len(args) != 1 {
			return usageError()
		}
		result, err := client.Commit()
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, result)
	case "abort":
		if len(args) != 1 {
			return usageError()
		}
		if err := client.Abort(); err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, map[string]bool{"aborted": true})
	case "put":
		resource, messages, options, err := parsePut(args[1:])
		if err != nil {
			return err
		}
		result, err := client.PutWithOptions(resource, messages, options)
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, result)
	case "scratch":
		resource, key, options, err := parseScratch(args[1:])
		if err != nil {
			return err
		}
		result, err := client.PutWithOptions(resource, map[string]conductor.MessageMutation{
			key: {Operation: conductor.MutationScratch},
		}, options)
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, result)
	case "get":
		request, err := parseGet(args[1:])
		if err != nil {
			return err
		}
		result, err := client.Get(request)
		if err != nil {
			return err
		}
		if err := conductor.WriteJSON(os.Stdout, result); err != nil {
			return err
		}
		return client.AcknowledgeRead(result)
	case "watch":
		if len(args) == 1 {
			return usageError()
		}
		if len(args) == 3 && args[1] == "--codex" {
			return runCodexWatchCommand(context.Background(), client, args[2])
		} else if len(args) == 3 && args[1] == "--codex-cli" {
			return runCodexCLIWatchCommand(context.Background(), client, args[2])
		} else if len(args) == 3 && args[1] == "--agy" {
			return runAgyWatchCommand(context.Background(), client, args[2])
		} else if len(args) == 3 && args[1] == "--agy-cli" {
			return runAgyCLIWatchCommand(context.Background(), client, args[2])
		} else if len(args) == 3 && args[1] == "--claude-cli" {
			return runClaudeCLIWatchCommand(context.Background(), client, args[2])
		} else if len(args) == 3 && args[1] == "--since" {
			since, err := strconv.ParseInt(args[2], 10, 64)
			if err != nil || since < 0 {
				return errors.New("--since requires a non-negative index")
			}
			return runOneShotWatch(context.Background(), client, since)
		}
		return usageError()
	case "channel":
		if len(args) != 3 || args[1] != "claude" {
			return usageError()
		}
		return runClaudeChannelCommand(context.Background(), client, args[2])
	default:
		return usageError()
	}
}

func runOneShotWatch(ctx context.Context, client *conductor.Client, since int64) error {
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	signal, err := client.WatchSinceContext(ctx, since)
	if err != nil {
		return err
	}
	if err := conductor.WriteJSON(os.Stdout, signal); err != nil {
		return err
	}
	return client.AcknowledgeSignal(signal)
}

func parseBegin(args []string) (string, conductor.WriteOptions, error) {
	if len(args) == 0 {
		return "", conductor.WriteOptions{}, usageError()
	}
	options := conductor.WriteOptions{}
	for index := 1; index < len(args); index++ {
		if args[index] != "--if-index" || index+1 >= len(args) {
			return "", conductor.WriteOptions{}, usageError()
		}
		if err := addExpectedIndex(&options, args[index+1]); err != nil {
			return "", conductor.WriteOptions{}, err
		}
		index++
	}
	return args[0], options, nil
}

func parsePut(args []string) (string, map[string]conductor.MessageMutation, conductor.WriteOptions, error) {
	if len(args) < 4 {
		return "", nil, conductor.WriteOptions{}, usageError()
	}
	messages := make(map[string]conductor.MessageMutation)
	options := conductor.WriteOptions{}
	for index := 1; index < len(args); index++ {
		if args[index] == "--if-index" {
			if index+1 >= len(args) {
				return "", nil, conductor.WriteOptions{}, usageError()
			}
			if err := addExpectedIndex(&options, args[index+1]); err != nil {
				return "", nil, conductor.WriteOptions{}, err
			}
			index++
			continue
		}
		if index+2 >= len(args) {
			return "", nil, conductor.WriteOptions{}, usageError()
		}
		key, kind, text := args[index], args[index+1], args[index+2]
		if key == "" || strings.HasPrefix(key, "--") {
			return "", nil, conductor.WriteOptions{}, fmt.Errorf("invalid message key %q", key)
		}
		if _, exists := messages[key]; exists {
			return "", nil, conductor.WriteOptions{}, fmt.Errorf("duplicate message key %s", key)
		}
		payload := conductor.MessagePayload{Text: text}
		messages[key] = conductor.MessageMutation{Operation: conductor.MutationSet, Kind: kind, Payload: &payload}
		index += 2
	}
	if len(messages) == 0 {
		return "", nil, conductor.WriteOptions{}, errors.New("put requires at least one message")
	}
	return args[0], messages, options, nil
}

func parseScratch(args []string) (string, string, conductor.WriteOptions, error) {
	if len(args) < 2 {
		return "", "", conductor.WriteOptions{}, usageError()
	}
	options := conductor.WriteOptions{}
	for index := 2; index < len(args); index++ {
		if args[index] != "--if-index" || index+1 >= len(args) {
			return "", "", conductor.WriteOptions{}, usageError()
		}
		if err := addExpectedIndex(&options, args[index+1]); err != nil {
			return "", "", conductor.WriteOptions{}, err
		}
		index++
	}
	return args[0], args[1], options, nil
}

func addExpectedIndex(options *conductor.WriteOptions, condition string) error {
	key, rawIndex, ok := strings.Cut(condition, "=")
	if !ok || key == "" || rawIndex == "" {
		return fmt.Errorf("invalid condition %q; expected key=index", condition)
	}
	index, err := strconv.ParseInt(rawIndex, 10, 64)
	if err != nil || index < 0 {
		return fmt.Errorf("invalid condition %q; index must be non-negative", condition)
	}
	if options.IfIndex == nil {
		options.IfIndex = make(map[string]int64)
	}
	if _, exists := options.IfIndex[key]; exists {
		return fmt.Errorf("duplicate expected index for %s", key)
	}
	options.IfIndex[key] = index
	return nil
}

func parseGet(args []string) (conductor.ReadRequest, error) {
	if len(args) == 0 {
		return conductor.ReadRequest{}, usageError()
	}
	request := conductor.ReadRequest{Resource: args[0], Mode: conductor.ReadDelta}
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--full":
			request.Mode = conductor.ReadFull
		case "--from", "--to":
			if index+1 >= len(args) {
				return conductor.ReadRequest{}, usageError()
			}
			value, err := strconv.ParseInt(args[index+1], 10, 64)
			if err != nil || value < 1 {
				return conductor.ReadRequest{}, fmt.Errorf("%s requires a positive index", args[index])
			}
			if args[index] == "--from" {
				request.From = value
			} else {
				request.To = value
			}
			request.Mode = conductor.ReadHistorical
			index++
		default:
			if strings.HasPrefix(args[index], "--") || request.Key != "" {
				return conductor.ReadRequest{}, usageError()
			}
			request.Key = args[index]
		}
	}
	if request.Mode == conductor.ReadHistorical && request.From == 0 {
		return conductor.ReadRequest{}, errors.New("historical mode requires --from")
	}
	if request.Mode == conductor.ReadHistorical && request.To > 0 && request.To < request.From {
		return conductor.ReadRequest{}, errors.New("--to must be greater than or equal to --from")
	}
	return request, nil
}

func usageError() error {
	return errors.New("usage: conductor install <absolute-skill-directory> | version | register <name> <responsibility> | deregister <name> | list-agents | begin <resource> [--if-index <key>=<index>]... | set <key> <kind> <text> | unset <key> | commit | abort | put <resource> <key> <kind> <text> [<key> <kind> <text>]... [--if-index <key>=<index>]... | scratch <resource> <key> [--if-index <key>=<index>]... | get <resource> [key] [--full | --from N [--to N]] | watch [--since N | --codex <name> | --codex-cli <name> | --agy <name> | --agy-cli <name> | --claude-cli <name>] | channel claude <name>")
}

func installUsageError() error {
	return errors.New("usage: conductor install <absolute-skill-directory>")
}

func currentVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

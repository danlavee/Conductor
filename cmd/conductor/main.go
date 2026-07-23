package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/install"
	"github.com/danlavee/Conductor/internal/migrate"
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
	case "migrate":
		if len(args) != 3 {
			return errors.New("usage: conductor migrate <absolute-source-root> <absolute-destination-root>")
		}
		version, err := migrate.DetectSourceVersion(args[1])
		if err != nil {
			return err
		}
		var report migrate.Report
		switch version {
		case 1:
			report, err = migrate.Run(args[1], args[2])
		case 2:
			report, err = migrate.RunV2ToV3(args[1], args[2])
		case 3:
			report, err = migrate.RunV3ToV4(args[1], args[2])
		default:
			err = fmt.Errorf("migrate supports v1, v2 or v3 source roots, found protocol %d", version)
		}
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, report)
	}
	if len(args) < 2 {
		return usageError()
	}
	agent := args[0]
	command := args[1]
	rest := args[2:]
	client, err := conductor.New(os.Getenv("CONDUCTOR_HOME"), agent)
	if err != nil {
		return err
	}
	switch command {
	case "join":
		if len(rest) > 1 {
			return usageError()
		}
		var responsibility string
		if len(rest) == 1 {
			responsibility = rest[0]
		}
		snapshot, err := client.Join(agent, responsibility)
		if err != nil {
			return err
		}
		if err := conductor.WriteJSON(os.Stdout, snapshot); err != nil {
			return err
		}
		return client.AcknowledgeSnapshot(snapshot)
	case "leave":
		if len(rest) != 0 {
			return usageError()
		}
		if err := client.Leave(agent); err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, map[string]string{"left": agent})
	case "list-agents":
		if len(rest) != 0 {
			return usageError()
		}
		agents, err := client.ListAgents()
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, agents)
	case "subscribe":
		if len(rest) != 1 {
			return usageError()
		}
		if group, ok := strings.CutPrefix(rest[0], "--topic-group="); ok {
			subscription, err := client.SubscribeTopicGroup(group)
			if err != nil {
				return err
			}
			return conductor.WriteJSON(os.Stdout, subscription)
		}
		if topic, ok := strings.CutPrefix(rest[0], "--topic="); ok {
			subscription, err := client.SubscribeTopic(topic)
			if err != nil {
				return err
			}
			return conductor.WriteJSON(os.Stdout, subscription)
		}
		return usageError()
	case "list":
		if len(rest) != 1 {
			return usageError()
		}
		if rest[0] == "--topic-groups" {
			groups, err := client.ListTopicGroups()
			if err != nil {
				return err
			}
			return conductor.WriteJSON(os.Stdout, groups)
		}
		if group, ok := strings.CutPrefix(rest[0], "--topic-group="); ok {
			topics, err := client.ListTopics(group)
			if err != nil {
				return err
			}
			return conductor.WriteJSON(os.Stdout, topics)
		}
		return usageError()
	case "begin":
		if len(rest) != 1 {
			return usageError()
		}
		if err := client.Begin(rest[0]); err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, map[string]string{"status": "begun", "topic": rest[0]})
	case "commit":
		if len(rest) != 0 {
			return usageError()
		}
		result, err := client.Commit()
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, result)
	case "abort":
		if len(rest) != 0 {
			return usageError()
		}
		if err := client.Abort(); err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, map[string]bool{"aborted": true})
	case "put":
		if len(rest) == 2 {
			if path, ok := strings.CutPrefix(rest[1], "--file="); ok {
				if path == "" {
					return usageError()
				}
				publication, err := putFile(client, rest[0], path)
				if err != nil {
					return err
				}
				return conductor.WriteJSON(os.Stdout, publication)
			}
		}
		var result conductor.Record
		var err error
		if len(rest) == 1 {
			result, err = client.StagePut(rest[0])
		} else if len(rest) == 2 {
			result, err = client.Put(rest[0], rest[1])
		} else {
			return usageError()
		}
		return writeRecordResult(result, err)
	case "edit":
		var result conductor.Record
		var err error
		if len(rest) == 2 {
			index, parseErr := parseRecordIndex(rest[0])
			if parseErr != nil {
				return parseErr
			}
			result, err = client.StageEdit(index, rest[1])
		} else if len(rest) == 3 {
			index, parseErr := parseRecordIndex(rest[1])
			if parseErr != nil {
				return parseErr
			}
			result, err = client.Edit(rest[0], index, rest[2])
		} else {
			return usageError()
		}
		return writeRecordResult(result, err)
	case "strike":
		var result conductor.Record
		var err error
		if len(rest) == 1 {
			index, parseErr := parseRecordIndex(rest[0])
			if parseErr != nil {
				return parseErr
			}
			result, err = client.StageStrike(index)
		} else if len(rest) == 2 {
			index, parseErr := parseRecordIndex(rest[1])
			if parseErr != nil {
				return parseErr
			}
			result, err = client.Strike(rest[0], index)
		} else {
			return usageError()
		}
		return writeRecordResult(result, err)
	case "get":
		request, err := parseGet(rest)
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
		if request.Mode == conductor.ReadDelta {
			return client.AcknowledgeRead(result)
		}
		return nil
	case "watch":
		watchArgs, modeValue, err := extractMode(rest)
		if err != nil {
			return err
		}
		mode, err := conductor.ParseDeliveryMode(modeValue)
		if err != nil {
			return err
		}
		if len(watchArgs) == 0 {
			return runOneShotWatch(context.Background(), client, mode)
		}
		if len(watchArgs) != 1 {
			return usageError()
		}
		switch watchArgs[0] {
		case "--claude-cli":
			return runClaudeCLIWatchCommand(context.Background(), client, agent, mode)
		}
		return usageError()
	case "channel":
		if len(rest) != 1 || rest[0] != "claude" {
			return usageError()
		}
		return runClaudeChannelCommand(context.Background(), client, agent)
	default:
		return usageError()
	}
}

// putFile bulk-loads a JSONL file (one JSON-encoded string per line, blank
// lines skipped) into topic as a single atomic transaction: one Begin, one
// StagePut per decoded line, one Commit — so subscribers see exactly one
// publish signal covering every line, the same as a manual begin/put×N/commit
// sequence. A decode failure or a staging failure aborts the open
// transaction before returning, so a partial file never leaves partial
// records or a dangling lock behind.
func putFile(client *conductor.Client, topic, path string) (conductor.Publication, error) {
	file, err := os.Open(path)
	if err != nil {
		return conductor.Publication{}, err
	}
	defer file.Close()
	if err := client.Begin(topic); err != nil {
		return conductor.Publication{}, err
	}
	staged := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var text string
		if err := json.Unmarshal([]byte(line), &text); err != nil {
			return conductor.Publication{}, abortWith(client, fmt.Errorf("%s: line is not a JSON string: %w", path, err))
		}
		if _, err := client.StagePut(text); err != nil {
			return conductor.Publication{}, abortWith(client, err)
		}
		staged++
	}
	if err := scanner.Err(); err != nil {
		return conductor.Publication{}, abortWith(client, err)
	}
	if staged == 0 {
		return conductor.Publication{}, abortWith(client, fmt.Errorf("%s: no non-blank lines to load", path))
	}
	return client.Commit()
}

// abortWith aborts the open transaction and folds any abort failure into the
// original cause, so a bulk-load failure never leaves a stuck transaction or
// topic lock behind.
func abortWith(client *conductor.Client, cause error) error {
	if abortErr := client.Abort(); abortErr != nil {
		return errors.Join(cause, fmt.Errorf("abort failed: %w", abortErr))
	}
	return cause
}

func writeRecordResult(record conductor.Record, operationErr error) error {
	if operationErr == nil {
		return conductor.WriteJSON(os.Stdout, record)
	}
	if record.Index == 0 {
		return operationErr
	}
	if writeErr := conductor.WriteJSON(os.Stdout, record); writeErr != nil {
		return errors.Join(operationErr, writeErr)
	}
	return operationErr
}

func runOneShotWatch(ctx context.Context, client *conductor.Client, mode conductor.DeliveryMode) error {
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	summary, err := client.WatchContext(ctx)
	if err != nil {
		return err
	}
	delivery, err := client.ResolveDelivery(summary, mode)
	if err != nil {
		return err
	}
	if mode != conductor.DeliveryContent {
		if err := conductor.WriteJSON(os.Stdout, summary); err != nil {
			return err
		}
	} else if err := conductor.WriteJSON(os.Stdout, delivery); err != nil {
		return err
	}
	return client.AcknowledgeDelivery(delivery)
}

// extractMode pulls an optional "--mode=<value>" argument out of watch.
func extractMode(args []string) ([]string, string, error) {
	rest := make([]string, 0, len(args))
	mode := ""
	for _, argument := range args {
		value, found := strings.CutPrefix(argument, "--mode=")
		if !found {
			rest = append(rest, argument)
			continue
		}
		if mode != "" {
			return nil, "", errors.New("--mode may only be given once")
		}
		if value == "" {
			return nil, "", errors.New("--mode requires a value")
		}
		mode = value
	}
	return rest, mode, nil
}

func parseRecordIndex(raw string) (int64, error) {
	index, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || index <= 0 {
		return 0, errors.New("record index must be a positive integer")
	}
	return index, nil
}

func parseGet(args []string) (conductor.ReadRequest, error) {
	if len(args) == 0 {
		return conductor.ReadRequest{}, usageError()
	}
	request := conductor.ReadRequest{Topic: args[0], Mode: conductor.ReadRange}
	modeSet := false
	hasBounds := false
	hasLimit := false
	for index := 1; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--delta" || argument == "--full":
			if modeSet {
				return conductor.ReadRequest{}, errors.New("get mode may only be given once")
			}
			modeSet = true
			if argument == "--delta" {
				request.Mode = conductor.ReadDelta
			} else {
				request.Mode = conductor.ReadFull
			}
		case strings.HasPrefix(argument, "--start="):
			value, err := strconv.ParseInt(strings.TrimPrefix(argument, "--start="), 10, 64)
			if err != nil || value < 0 {
				return conductor.ReadRequest{}, errors.New("--start requires a non-negative index")
			}
			request.Start = value
			hasBounds = true
		case strings.HasPrefix(argument, "--end="):
			value, err := strconv.ParseInt(strings.TrimPrefix(argument, "--end="), 10, 64)
			if err != nil || value < 0 {
				return conductor.ReadRequest{}, errors.New("--end requires a non-negative index")
			}
			request.End = value
			hasBounds = true
		case strings.HasPrefix(argument, "--limit="):
			value, err := strconv.Atoi(strings.TrimPrefix(argument, "--limit="))
			if err != nil || value <= 0 {
				return conductor.ReadRequest{}, errors.New("--limit requires a positive integer")
			}
			request.Limit = value
			hasLimit = true
		default:
			if strings.HasPrefix(argument, "--") || request.RecordIndex != 0 {
				return conductor.ReadRequest{}, usageError()
			}
			value, err := parseRecordIndex(argument)
			if err != nil {
				return conductor.ReadRequest{}, err
			}
			request.RecordIndex = value
		}
	}
	if request.End > 0 && request.End < request.Start {
		return conductor.ReadRequest{}, errors.New("--end must be greater than or equal to --start")
	}
	if request.Mode == conductor.ReadDelta && hasBounds {
		return conductor.ReadRequest{}, errors.New("--start and --end cannot be used with --delta")
	}
	if request.Mode == conductor.ReadFull && (hasBounds || hasLimit) {
		return conductor.ReadRequest{}, errors.New("range options cannot be used with --full")
	}
	return request, nil
}

func usageError() error {
	return errors.New("usage: conductor install <absolute-skill-directory> | conductor migrate <absolute-source-root> <absolute-destination-root> | conductor version | conductor <agent> join [responsibility] | conductor <agent> leave | conductor <agent> list-agents | conductor <agent> subscribe (--topic-group=<group> | --topic=<group/topic>) | conductor <agent> list (--topic-groups | --topic-group=<group>) | conductor <agent> begin <group/topic> | conductor <agent> put <group/topic> <text> | conductor <agent> put <group/topic> --file=<path> | conductor <agent> put <text> | conductor <agent> edit <group/topic> <index> <text> | conductor <agent> edit <index> <text> | conductor <agent> strike <group/topic> <index> | conductor <agent> strike <index> | conductor <agent> commit | conductor <agent> abort | conductor <agent> get <group/topic> [index] ([--start=N] [--end=N] [--limit=N] | --delta [--limit=N] | --full) | conductor <agent> watch [--claude-cli] [--mode=summary|content] | conductor <agent> channel claude")
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

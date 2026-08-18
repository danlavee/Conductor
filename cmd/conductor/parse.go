package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	conductor "github.com/danlavee/Conductor"
)

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

func parseRedact(args []string) (topic string, start, end int64, err error) {
	if len(args) < 2 || len(args) > 3 {
		return "", 0, 0, usageError()
	}
	topic = args[0]
	if len(args) == 2 {
		index, err := parseRecordIndex(args[1])
		if err != nil {
			return "", 0, 0, err
		}
		return topic, index, index, nil
	}

	for _, argument := range args[1:] {
		if value, ok := strings.CutPrefix(argument, "--start="); ok {
			start, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return "", 0, 0, fmt.Errorf("invalid --start: %w", err)
			}
		} else if value, ok := strings.CutPrefix(argument, "--end="); ok {
			end, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return "", 0, 0, fmt.Errorf("invalid --end: %w", err)
			}
		} else {
			return "", 0, 0, usageError()
		}
	}
	if start <= 0 || end <= 0 || end < start {
		return "", 0, 0, errors.New("invalid redact range")
	}
	return topic, start, end, nil
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
	return errors.New("usage: conductor install <absolute-skill-directory> | conductor verify <absolute-skill-directory> | conductor cutover <status|freeze|replace|activate|abort> ... | conductor migrate <absolute-source-root> <absolute-destination-root> | conductor version | conductor <agent> join [responsibility] | conductor <agent> leave | conductor <agent> list-agents | conductor <agent> subscribe (--topic-group=<group> | --topic=<group/topic>) | conductor <agent> list (--topic-groups | --topic-group=<group>) | conductor <agent> begin <group/topic> | conductor <agent> put <group/topic> <text> | conductor <agent> put <group/topic> --file=<path> | conductor <agent> put <text> | conductor <agent> edit <group/topic> <index> <text> | conductor <agent> edit <index> <text> | conductor <agent> strike <group/topic> <index> | conductor <agent> strike <index> | conductor <agent> commit | conductor <agent> abort | conductor <agent> get <group/topic> [index] ([--start=N] [--end=N] [--limit=N] | --delta [--limit=N] | --full) | conductor <agent> watch [--codex-desktop | --claude-cli] [--mode=summary|content]")
}

func installUsageError() error {
	return errors.New("usage: conductor install <absolute-skill-directory>")
}

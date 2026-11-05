package main

import (
	"errors"
	"strconv"
	"strings"

	conductor "github.com/danlavee/Conductor/internal/state"
)

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

// usageText is the one statement of the surface, reached two ways: printed on
// request, and carried by the error a malformed command returns.
const usageText = "usage: conductor install <absolute-skill-directory> [--replace] | conductor verify <absolute-skill-directory> | conductor adapter claude <arm|release> --session=<id> | conductor adapter claude identity [--session=<id>] | conductor adapter claude bind <agent> | conductor adapter claude install <absolute-adapter-directory> [--replace] | conductor adapter grok <identity|reside|stop|release|reannounce> --session=<id> | conductor adapter grok bind <agent> [--session=<id>] | conductor adapter grok install <absolute-adapter-directory> [--replace] | conductor adapter agy <identity|stop> [--hook] [--session=<id>] | conductor adapter agy reside --session=<id> | conductor adapter agy release [--session=<id>] | conductor adapter agy bind <agent> [--session=<id>] | conductor adapter agy takeover --agent=<agent> [--confirm=<token>] | conductor adapter agy install <absolute-adapter-directory> [--replace] | conductor version | conductor <agent> join [responsibility] | conductor <agent> leave | conductor <agent> subscribe (--topic-group=<group> | --topic=<group/topic>) | conductor <agent> list (--topic-groups | --topic-group=<group>) | conductor <agent> put <group/topic> (<text> | --file=<path>) | conductor <agent> put <group/topic> --jsonl=<path> | conductor <agent> edit <group/topic> <index> (<text> | --file=<path>) | conductor <agent> get <group/topic> [index] ([--start=N] [--end=N] [--limit=N] | --delta [--limit=N] | --full) | conductor <agent> watch | conductor <agent> status"

// usageResult keeps the surface on the same wire as every other answer. stdout
// carries JSON and nothing else, so a reader that decodes every response needs
// no special case for the one command whose subject is the others.
type usageResult struct {
	Usage string `json:"usage"`
}

func usageError() error {
	return errors.New(usageText)
}

// helpRequested reads a help token as a request for the surface in the first
// two positions and as an operand everywhere after. First covers the bare
// binary; second covers both an identity with nothing after it and a reserved
// command asked what it takes, neither of which has a real operand there. The
// cutoff is what matters: `put` takes arbitrary text from the third position
// on, and answering that with usage would swallow a publication.
func helpRequested(args []string) bool {
	switch len(args) {
	case 1:
		return isHelpToken(args[0])
	case 2:
		return isHelpToken(args[1])
	}
	return false
}

func isHelpToken(argument string) bool {
	switch argument {
	case "--help", "-h", "help":
		return true
	}
	return false
}

func installUsageError() error {
	return errors.New("usage: conductor install <absolute-skill-directory> [--replace]")
}

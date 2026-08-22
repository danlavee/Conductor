package main

import (
	"errors"
	"os"
	"strconv"
	"strings"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/adapters/claude"
)

func runAgentCommand(agent, command string, rest []string) error {
	open := conductor.New
	// Neither may bring a protocol root into being as a side effect of being
	// asked: watch attaches to a bus that must already exist, and status
	// answers about one that may never have. Written as a condition rather
	// than a switch on command, which skillcheck reads as a dispatch point.
	if command == "watch" || command == "status" {
		open = conductor.Open
	}
	client, err := open(os.Getenv("CONDUCTOR_HOME"), agent)
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
	// Every record verb names its topic. There is no second, topic-less form:
	// the staged one existed only inside a transaction the CLI no longer
	// opens, and its absence is what makes each of these one unambiguous
	// shape rather than two distinguished by argument count.
	case "put":
		if len(rest) != 2 {
			return usageError()
		}
		if path, ok := strings.CutPrefix(rest[1], jsonlPrefix); ok {
			if path == "" {
				return usageError()
			}
			publication, err := putJSONL(client, rest[0], path)
			if err != nil {
				return err
			}
			return conductor.WriteJSON(os.Stdout, publication)
		}
		text, err := resolveText(rest[1])
		if err != nil {
			return err
		}
		record, err := client.Put(rest[0], text)
		return writeRecordResult(record, err)
	case "edit":
		if len(rest) != 3 {
			return usageError()
		}
		index, err := parseRecordIndex(rest[1])
		if err != nil {
			return err
		}
		text, err := resolveText(rest[2])
		if err != nil {
			return err
		}
		record, err := client.Edit(rest[0], index, text)
		return writeRecordResult(record, err)
	case "strike":
		if len(rest) != 2 {
			return usageError()
		}
		index, err := parseRecordIndex(rest[1])
		if err != nil {
			return err
		}
		record, err := client.Strike(rest[0], index)
		return writeRecordResult(record, err)
	case "redact":
		topic, start, end, err := parseRedact(rest)
		if err != nil {
			return err
		}
		if err := client.Redact(topic, start, end); err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, map[string]string{
			"status": "redacted",
			"topic":  topic,
			"start":  strconv.FormatInt(start, 10),
			"end":    strconv.FormatInt(end, 10),
		})
	case "get":
		request, err := parseGet(rest)
		if err != nil {
			return err
		}
		readResult, err := client.Get(request)
		if err != nil {
			return err
		}
		if err := conductor.WriteJSON(os.Stdout, readResult); err != nil {
			return err
		}
		if request.Mode == conductor.ReadDelta {
			return client.AcknowledgeRead(readResult)
		}
		return nil
	case "watch":
		return runWatchCommand(client, rest)
	case "status":
		if len(rest) != 0 {
			return usageError()
		}
		adapter, err := hostAdapter(client.Home)
		if err != nil {
			return err
		}
		return writeParticipation(client, adapter)
	default:
		return usageError()
	}
}

// writeParticipation emits the participation answer. Both callers are acts the
// agent concludes from -- asking, and arming -- and they must answer with the
// same fact in the same shape, or the agent has two accounts of its own
// reachability and no way to tell which one it is holding.
//
// The host's adapter is resolved by the caller rather than here, because the
// two differ in when they can afford to fail: asking may fail at any point,
// while arming must find out whether it can answer before it takes the stream.
func writeParticipation(client *conductor.Client, adapter *hostAdapterReport) error {
	status, err := client.WatchStatus()
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, statusReport{WatchStatus: status, Adapter: adapter})
}

// statusReport is the whole participation answer. WatchStatus carries what the
// bus knows; an adapter's binding is host-side, and composing it here is what
// keeps internal/state from importing an adapter to answer a question about
// one.
type statusReport struct {
	conductor.WatchStatus
	Adapter *hostAdapterReport `json:"adapter,omitempty"`
}

// hostAdapterReport separates three host states that wakeability alone collapses
// into two: no adapter here, an adapter holding an identity for this session,
// and an adapter present that has resolved none -- the last of which is
// indistinguishable from the first in every field that existed before it.
type hostAdapterReport struct {
	Name   string `json:"name"`
	Agent  string `json:"agent,omitempty"`
	Source string `json:"source"`
}

// hostAdapter reports the adapter serving this process, or nothing where none
// does. A served session exports its identifier into every process it starts,
// so an absent one is the answer that no adapter is here rather than a failed
// read of one that is. Every other failure is returned: status exists to be
// trustworthy when the rest of the bus is not, and an adapter it could not read
// reported as an adapter that is not there is the exact confusion it is being
// changed to remove.
func hostAdapter(root string) (*hostAdapterReport, error) {
	session := os.Getenv(claude.SessionEnvironment)
	if session == "" {
		return nil, nil
	}
	if err := claude.ValidateSession(session); err != nil {
		return nil, err
	}
	working, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	project, err := claude.ProjectDirectory(working)
	if err != nil {
		return nil, err
	}
	binding, err := claude.ResolveBinding(root, session, project)
	if err != nil {
		return nil, err
	}
	report := &hostAdapterReport{Name: "claude", Agent: binding.Agent, Source: string(binding.Source)}
	if binding.Source == "" {
		report.Source = string(claude.NoBinding)
	}
	return report, nil
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

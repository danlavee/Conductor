package main

import (
	"os"
	"strings"

	conductor "github.com/danlavee/Conductor/internal/state"
)

func runAgentCommand(agent, command string, rest []string) error {
	open := conductor.New
	// Neither may bring a protocol root into being as a side effect of being
	// asked: watch attaches to a bus that must already exist, and status
	// answers about one that may never have.
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
	// Every record verb names its topic and publishes at once.
	case "put":
		if len(rest) != 2 {
			return usageError()
		}
		if path, ok := strings.CutPrefix(rest[1], jsonlPrefix); ok {
			if path == "" {
				return usageError()
			}
			return writeDispatch(putJSONL(client, rest[0], path))
		}
		text, err := resolveText(rest[1])
		if err != nil {
			return err
		}
		return writeDispatch(client.Put(rest[0], text))
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
		return writeDispatch(client.Edit(rest[0], index, text))
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
		return writeStatus(client)
	default:
		return usageError()
	}
}

// writeStatus emits the participation answer. Asking and arming both answer
// with it, so the agent has one account of its own reachability.
func writeStatus(client *conductor.Client) error {
	status, err := client.WatchStatus()
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, status)
}

// writeDispatch is the single answer every record verb gives. Taking the whole
// call result rather than a record keeps the recipients attached to the act
// that resolved them: an agent that sees only what it wrote cannot tell a
// topic somebody reads from one nobody is subscribed to, and both look like a
// success.
func writeDispatch(dispatch conductor.Dispatch, err error) error {
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, dispatch)
}

package main

import (
	"errors"
	"os"
	"strconv"
	"strings"

	conductor "github.com/danlavee/Conductor"
)

func runAgentCommand(agent, command string, rest []string) error {
	open := conductor.New
	if command == "watch" {
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
		publication, err := client.Commit()
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, publication)
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
		var record conductor.Record
		var err error
		if len(rest) == 1 {
			record, err = client.StagePut(rest[0])
		} else if len(rest) == 2 {
			record, err = client.Put(rest[0], rest[1])
		} else {
			return usageError()
		}
		return writeRecordResult(record, err)
	case "edit":
		var record conductor.Record
		var err error
		if len(rest) == 2 {
			index, parseErr := parseRecordIndex(rest[0])
			if parseErr != nil {
				return parseErr
			}
			record, err = client.StageEdit(index, rest[1])
		} else if len(rest) == 3 {
			index, parseErr := parseRecordIndex(rest[1])
			if parseErr != nil {
				return parseErr
			}
			record, err = client.Edit(rest[0], index, rest[2])
		} else {
			return usageError()
		}
		return writeRecordResult(record, err)
	case "strike":
		var record conductor.Record
		var err error
		if len(rest) == 1 {
			index, parseErr := parseRecordIndex(rest[0])
			if parseErr != nil {
				return parseErr
			}
			record, err = client.StageStrike(index)
		} else if len(rest) == 2 {
			index, parseErr := parseRecordIndex(rest[1])
			if parseErr != nil {
				return parseErr
			}
			record, err = client.Strike(rest[0], index)
		} else {
			return usageError()
		}
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
		status, err := client.WatchStatus()
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, status)
	default:
		return usageError()
	}
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

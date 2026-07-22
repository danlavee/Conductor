// Package migrate performs explicit, non-destructive state protocol migrations.
package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/state"
	"github.com/danlavee/Conductor/protocol"
)

type Report struct {
	FromVersion    int       `json:"from_version"`
	ToVersion      int       `json:"to_version"`
	Source         string    `json:"source"`
	Destination    string    `json:"destination"`
	Agents         int       `json:"agents"`
	Topics         int       `json:"topics"`
	Records        int       `json:"records"`
	DiscardedKinds int       `json:"discarded_kinds"`
	SkippedScratch int       `json:"skipped_scratch"`
	Mappings       []Mapping `json:"mappings"`
}

type Mapping struct {
	Topic string `json:"topic"`
	Key   string `json:"key"`
	Index int64  `json:"index"`
}

type legacyPayload struct {
	Text string `json:"text"`
}

type legacyMutation struct {
	Operation string         `json:"operation"`
	Kind      string         `json:"kind"`
	Payload   *legacyPayload `json:"payload"`
}

type legacyPublication struct {
	Index     int64                     `json:"index"`
	Resource  string                    `json:"resource"`
	Agent     string                    `json:"agent"`
	Timestamp time.Time                 `json:"timestamp"`
	Messages  map[string]legacyMutation `json:"messages"`
}

// Run migrates one v1 root to a distinct new v2 root. The source is read-only.
func Run(source, destination string) (Report, error) {
	report := Report{FromVersion: 1, ToVersion: state.CurrentProtocolVersion}
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return report, errors.New("migration source and destination must be absolute paths")
	}
	source, destination = filepath.Clean(source), filepath.Clean(destination)
	report.Source, report.Destination = source, destination
	if strings.EqualFold(source, destination) {
		return report, errors.New("migration destination must differ from source")
	}
	if err := requireProtocol(source, 1); err != nil {
		return report, err
	}
	if err := rejectTransactions(source); err != nil {
		return report, err
	}
	destinationExists, err := requireEmptyDestination(destination)
	if err != nil {
		return report, err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return report, err
	}
	stage, err := os.MkdirTemp(parent, ".conductor-migrate-*")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(stage)
	if _, err := state.New(stage, ""); err != nil {
		return report, err
	}
	agents, err := migrateAgents(source, stage)
	if err != nil {
		return report, err
	}
	report.Agents = len(agents)
	topics, mappings, records, discarded, skipped, highSequence, err := migrateTopics(source, stage)
	if err != nil {
		return report, err
	}
	report.Topics, report.Mappings, report.Records = len(topics), mappings, records
	report.DiscardedKinds, report.SkippedScratch = discarded, skipped
	if sourceHigh, err := readLegacyHighSequence(source); err != nil {
		return report, err
	} else if sourceHigh > highSequence {
		highSequence = sourceHigh
	}
	if err := writeJSON(filepath.Join(stage, "state", "index.json"), map[string]int64{"index": highSequence}); err != nil {
		return report, err
	}
	for _, agent := range agents {
		if err := writeJSON(filepath.Join(stage, "subscriptions", agent.Name+".json"), state.Subscription{Topics: topics, TopicGroups: []string{}}); err != nil {
			return report, err
		}
	}
	if err := writeJSON(filepath.Join(stage, "migration-report.json"), report); err != nil {
		return report, err
	}
	if destinationExists {
		if err := os.Remove(destination); err != nil {
			return report, err
		}
	}
	if err := os.Rename(stage, destination); err != nil {
		return report, err
	}
	return report, nil
}

func migrateAgents(source, destination string) ([]protocol.Agent, error) {
	entries, err := os.ReadDir(filepath.Join(source, "registry"))
	if err != nil {
		return nil, err
	}
	agents := []protocol.Agent{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var agent protocol.Agent
		if err := readJSON(filepath.Join(source, "registry", entry.Name()), &agent); err != nil {
			return nil, err
		}
		if err := writeJSON(filepath.Join(destination, "registry", entry.Name()), agent); err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents, nil
}

func migrateTopics(source, destination string) ([]string, []Mapping, int, int, int, int64, error) {
	publications, err := readLegacyPublications(source)
	if err != nil {
		return nil, nil, 0, 0, 0, 0, err
	}
	topicKeys := map[string]map[string]int64{}
	current := map[string]map[string]string{}
	topicHeads := map[string]int64{}
	var mappings []Mapping
	var recordCount, discarded, skipped int
	var highSequence int64
	for _, publication := range publications {
		if publication.Index > highSequence {
			highSequence = publication.Index
		}
		keys := make([]string, 0, len(publication.Messages))
		for key := range publication.Messages {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if topicKeys[publication.Resource] == nil {
			topicKeys[publication.Resource] = map[string]int64{}
			current[publication.Resource] = map[string]string{}
		}
		records := []protocol.Record{}
		for _, key := range keys {
			mutation := publication.Messages[key]
			switch mutation.Operation {
			case "set":
				if mutation.Payload == nil {
					return nil, nil, 0, 0, 0, 0, fmt.Errorf("v1 set %s/%s has no payload", publication.Resource, key)
				}
				index := topicKeys[publication.Resource][key]
				if index == 0 {
					index = int64(len(topicKeys[publication.Resource]) + 1)
					topicKeys[publication.Resource][key] = index
					mappings = append(mappings, Mapping{Topic: publication.Resource, Key: key, Index: index})
					recordCount++
				}
				current[publication.Resource][key] = mutation.Payload.Text
				records = append(records, protocol.Record{Index: index, Text: mutation.Payload.Text})
				if mutation.Kind != "" {
					discarded++
				}
			case "scratch":
				text, exists := current[publication.Resource][key]
				if !exists {
					skipped++
					continue
				}
				text = "~~" + text + "~~"
				current[publication.Resource][key] = text
				records = append(records, protocol.Record{Index: topicKeys[publication.Resource][key], Text: text})
			default:
				return nil, nil, 0, 0, 0, 0, fmt.Errorf("unknown v1 operation %q", mutation.Operation)
			}
		}
		if len(records) == 0 {
			continue
		}
		topicDir, err := topicPath(destination, publication.Resource)
		if err != nil {
			return nil, nil, 0, 0, 0, 0, err
		}
		if err := writeJSON(filepath.Join(topicDir, "history", fmt.Sprintf("%020d.json", publication.Index)), protocol.Publication{Sequence: publication.Index, Topic: publication.Resource, Agent: publication.Agent, Timestamp: publication.Timestamp, Records: records}); err != nil {
			return nil, nil, 0, 0, 0, 0, err
		}
		topicHeads[publication.Resource] = publication.Index
	}
	topics := make([]string, 0, len(topicHeads))
	for topic, head := range topicHeads {
		topicDir, _ := topicPath(destination, topic)
		if err := writeJSON(filepath.Join(topicDir, "head.json"), map[string]int64{"sequence": head}); err != nil {
			return nil, nil, 0, 0, 0, 0, err
		}
		if err := writeJSON(filepath.Join(topicDir, "record-index.json"), map[string]int64{"index": int64(len(topicKeys[topic]))}); err != nil {
			return nil, nil, 0, 0, 0, 0, err
		}
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	sort.Slice(mappings, func(i, j int) bool {
		return mappings[i].Topic < mappings[j].Topic || mappings[i].Topic == mappings[j].Topic && mappings[i].Index < mappings[j].Index
	})
	return topics, mappings, recordCount, discarded, skipped, highSequence, nil
}

func readLegacyPublications(source string) ([]legacyPublication, error) {
	var result []legacyPublication
	groups, err := os.ReadDir(filepath.Join(source, "topics"))
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		if !group.IsDir() {
			continue
		}
		topics, err := os.ReadDir(filepath.Join(source, "topics", group.Name()))
		if err != nil {
			return nil, err
		}
		for _, topic := range topics {
			if !topic.IsDir() {
				continue
			}
			history := filepath.Join(source, "topics", group.Name(), topic.Name(), "history")
			files, err := os.ReadDir(history)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, file := range files {
				if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
					continue
				}
				var publication legacyPublication
				if err := readJSON(filepath.Join(history, file.Name()), &publication); err != nil {
					return nil, err
				}
				result = append(result, publication)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result, nil
}

func topicPath(root, topic string) (string, error) {
	parts := strings.Split(topic, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid v1 resource %q", topic)
	}
	return filepath.Join(root, "topics", parts[0], parts[1]), nil
}

func requireProtocol(root string, version int) error {
	var document struct {
		Version int `json:"version"`
	}
	if err := readJSON(filepath.Join(root, "protocol.json"), &document); err != nil {
		return err
	}
	if document.Version != version {
		return fmt.Errorf("migration requires protocol v%d source", version)
	}
	return nil
}

func rejectTransactions(root string) error {
	entries, err := os.ReadDir(filepath.Join(root, "transactions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			return errors.New("migration requires all v1 transactions to be committed or aborted")
		}
	}
	return nil
}

func requireEmptyDestination(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(entries) != 0 {
		return true, errors.New("migration destination must be missing or empty")
	}
	return true, nil
}

func readLegacyHighSequence(root string) (int64, error) {
	var value struct {
		Index int64 `json:"index"`
	}
	err := readJSON(filepath.Join(root, "state", "index.json"), &value)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return value.Index, err
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

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

type v1TopicMigration struct {
	topics         []string
	mappings       []Mapping
	records        int
	discardedKinds int
	skippedScratch int
	highSequence   int64
}

// runV1ToV4 migrates one v1 root directly to a distinct v4 root. The source is read-only.
func runV1ToV4(source, destination string) (report Report, resultErr error) {
	preparation, err := prepareMigration(source, destination, 1, protocolVersion4)
	report = preparation.report
	if err != nil {
		return report, err
	}
	defer cleanupStage(preparation.stage, &resultErr)

	if err := initializeV4Root(preparation.stage); err != nil {
		return report, err
	}
	agents, err := migrateAgents(preparation.source, preparation.stage)
	if err != nil {
		return report, err
	}
	report.Agents = len(agents)
	topicMigration, err := migrateV1Topics(preparation.source, preparation.stage)
	if err != nil {
		return report, err
	}
	report.Topics = len(topicMigration.topics)
	report.Mappings = topicMigration.mappings
	report.Records = topicMigration.records
	report.DiscardedKinds = topicMigration.discardedKinds
	report.SkippedScratch = topicMigration.skippedScratch
	if sourceHigh, err := readLegacyHighSequence(preparation.source); err != nil {
		return report, err
	} else if sourceHigh > topicMigration.highSequence {
		topicMigration.highSequence = sourceHigh
	}
	if err := writeJSON(filepath.Join(preparation.stage, "state", "index.json"), map[string]int64{"index": topicMigration.highSequence}); err != nil {
		return report, err
	}
	for _, agent := range agents {
		if err := writeJSON(filepath.Join(preparation.stage, "subscriptions", agent.Name+".json"), state.Subscription{Topics: topicMigration.topics, TopicGroups: []string{}}); err != nil {
			return report, err
		}
	}
	if err := writeJSON(filepath.Join(preparation.stage, "migration-report.json"), report); err != nil {
		return report, err
	}
	if err := publishStage(preparation.stage, preparation.destination, preparation.destinationExists); err != nil {
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

func migrateV1Topics(source, destination string) (v1TopicMigration, error) {
	publications, err := readLegacyPublications(source)
	if err != nil {
		return v1TopicMigration{}, err
	}
	topicKeys := map[string]map[string]int64{}
	current := map[string]map[string]string{}
	topicHeads := map[string]int64{}
	var result v1TopicMigration
	for _, publication := range publications {
		if publication.Index > result.highSequence {
			result.highSequence = publication.Index
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
					return v1TopicMigration{}, fmt.Errorf("v1 set %s/%s has no payload", publication.Resource, key)
				}
				index := topicKeys[publication.Resource][key]
				if index == 0 {
					index = int64(len(topicKeys[publication.Resource]) + 1)
					topicKeys[publication.Resource][key] = index
					result.mappings = append(result.mappings, Mapping{Topic: publication.Resource, Key: key, Index: index})
					result.records++
				}
				current[publication.Resource][key] = mutation.Payload.Text
				records = append(records, protocol.Record{Index: index, Text: mutation.Payload.Text})
				if mutation.Kind != "" {
					result.discardedKinds++
				}
			case "scratch":
				text, exists := current[publication.Resource][key]
				if !exists {
					result.skippedScratch++
					continue
				}
				text = "~~" + text + "~~"
				current[publication.Resource][key] = text
				records = append(records, protocol.Record{Index: topicKeys[publication.Resource][key], Text: text})
			default:
				return v1TopicMigration{}, fmt.Errorf("unknown v1 operation %q", mutation.Operation)
			}
		}
		if len(records) == 0 {
			continue
		}
		topicDir, err := topicPath(destination, publication.Resource)
		if err != nil {
			return v1TopicMigration{}, err
		}
		entry := protocol.Publication{
			Sequence:  publication.Index,
			Topic:     publication.Resource,
			Agent:     publication.Agent,
			Timestamp: publication.Timestamp,
			Records:   records,
		}
		if err := appendJSONLine(filepath.Join(topicDir, "history.jsonl"), entry); err != nil {
			return v1TopicMigration{}, err
		}
		topicHeads[publication.Resource] = publication.Index
	}
	result.topics = make([]string, 0, len(topicHeads))
	for topic, head := range topicHeads {
		topicDir, _ := topicPath(destination, topic)
		if err := writeJSON(filepath.Join(topicDir, "head.json"), map[string]int64{"sequence": head}); err != nil {
			return v1TopicMigration{}, err
		}
		if err := writeJSON(filepath.Join(topicDir, "record-index.json"), map[string]int64{"index": int64(len(topicKeys[topic]))}); err != nil {
			return v1TopicMigration{}, err
		}
		result.topics = append(result.topics, topic)
	}
	sort.Strings(result.topics)
	sort.Slice(result.mappings, func(i, j int) bool {
		return result.mappings[i].Topic < result.mappings[j].Topic ||
			result.mappings[i].Topic == result.mappings[j].Topic && result.mappings[i].Index < result.mappings[j].Index
	})
	return result, nil
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

func appendJSONLine(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

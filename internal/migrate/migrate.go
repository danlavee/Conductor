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
	// The fields below are set only by RunV2ToV3: how many files of each
	// kind had v2 field names translated to their v3 equivalents.
	CursorsMigrated      int `json:"cursors_migrated,omitempty"`
	EventsMigrated       int `json:"events_migrated,omitempty"`
	PublicationsMigrated int `json:"publications_migrated,omitempty"`
	TopicHeadsMigrated   int `json:"topic_heads_migrated,omitempty"`
	InboxLinesMigrated   int `json:"inbox_lines_migrated,omitempty"`
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

// legacyCursor is Cursor's v2 wire shape, before the v3 rename that shipped
// without a protocol bump: signal_index -> summary_sequence,
// resource_indexes -> topic_sequences, signal_ranges -> summary_ranges.
// inbox_offset is unchanged. It is decoded with plain encoding/json (not the
// strict, current state.Cursor), since old-shape files predate the new
// field names and would be rejected by DisallowUnknownFields.
type legacyCursor struct {
	Signal       int64              `json:"signal_index"`
	SignalRanges []state.IndexRange `json:"signal_ranges,omitempty"`
	InboxOffset  int64              `json:"inbox_offset"`
	Resources    map[string]int64   `json:"resource_indexes"`
}

// legacySummary is Summary's v2 wire shape, before an earlier terminology
// cleanup (resource -> topic, index -> sequence) that -- like the cursor
// rename -- shipped inside the same "v2" lifetime without ever bumping
// CurrentProtocolVersion. A real production v2 root was found containing
// both generations of field names, so both Resource/Topic and Index/Sequence
// are accepted here and whichever is present wins.
//
// The old shape also carried a "key" field with no equivalent in the
// current Summary. Inspection of a real production event log showed it is
// always fully redundant: for membership signals (type join/leave) key was
// always identical to agent; for content signals (type update) key was
// always the fixed placeholder "*". Neither case carries information not
// already recoverable from type/topic/agent, so key is dropped rather than
// carried forward.
type legacySummary struct {
	Type     string `json:"type"`
	Resource string `json:"resource,omitempty"`
	Topic    string `json:"topic,omitempty"`
	Key      string `json:"key,omitempty"`
	Index    int64  `json:"index,omitempty"`
	Sequence int64  `json:"sequence,omitempty"`
	Agent    string `json:"agent"`
}

func (s legacySummary) topic() string {
	if s.Topic != "" {
		return s.Topic
	}
	return s.Resource
}

func (s legacySummary) sequence() int64 {
	if s.Sequence != 0 {
		return s.Sequence
	}
	return s.Index
}

func translateLegacySummary(legacy legacySummary) state.Summary {
	return state.Summary{
		Type:     legacy.Type,
		Topic:    legacy.topic(),
		Sequence: legacy.sequence(),
		Agent:    legacy.Agent,
	}
}

// legacyEvent is Event's v2 wire shape: the inner signal carries
// legacySummary's older field names (see above), and its own field was
// named "signal" before being renamed to "summary". Both names are accepted
// here since either generation may appear in a real v2 root.
type legacyEvent struct {
	Signal     *legacySummary `json:"signal,omitempty"`
	Summary    *legacySummary `json:"summary,omitempty"`
	Recipients []string       `json:"recipients"`
}

// inboxLineLength pairs one inbox line's old (source) and new (translated)
// byte length, newline included. cursor.InboxOffset is a byte offset into
// this file, so translating the file's content without also translating any
// offset that points into it would silently point at the wrong byte
// position the moment a line's encoded length changes (dropping "key"
// always shortens a line) -- not a decode error, but a real correctness bug
// (replayed or skipped deliveries) distinct from the field-rename class this
// migration otherwise handles. See translateInboxOffset.
type inboxLineLength struct {
	old int64
	new int64
}

// translateInboxContent rewrites an inbox/<agent> file: a newline-delimited
// (JSONL) log of compact Summary values, one per unread delivery, appended
// to by writeEvent for every recipient of a signal. Each line decodes as
// legacySummary and re-encodes as the current, compact Summary shape,
// preserving line order and count. It returns the rebuilt file content and
// each line's old/new length, for translateInboxOffset to consume.
func translateInboxContent(data []byte) ([]byte, []inboxLineLength, error) {
	var rebuilt strings.Builder
	var lengths []inboxLineLength
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var legacy legacySummary
		if err := json.Unmarshal([]byte(line), &legacy); err != nil {
			return nil, nil, err
		}
		encoded, err := json.Marshal(translateLegacySummary(legacy))
		if err != nil {
			return nil, nil, err
		}
		rebuilt.Write(encoded)
		rebuilt.WriteByte('\n')
		lengths = append(lengths, inboxLineLength{old: int64(len(line)) + 1, new: int64(len(encoded)) + 1})
	}
	return []byte(rebuilt.String()), lengths, nil
}

// translateInboxOffset maps a byte offset into the original (source) inbox
// file to the equivalent offset into the translated file, by walking whole
// lines: an offset only ever lands on a line boundary (nextInboxSummary and
// inboxOffsetThrough both advance it by exactly one full line's length), so
// summing old lengths until reaching oldOffset and returning the matching
// sum of new lengths preserves "how many lines already consumed" exactly.
func translateInboxOffset(lengths []inboxLineLength, oldOffset int64) int64 {
	var oldConsumed, newConsumed int64
	for _, length := range lengths {
		if oldConsumed >= oldOffset {
			break
		}
		oldConsumed += length.old
		newConsumed += length.new
	}
	return newConsumed
}

// legacyPublicationV2 is Publication's v2 wire shape, before the same
// terminology cleanup that affected Summary: index -> sequence, resource ->
// topic. The nested Record shape was already identical to today's, so
// Records decodes and copies through unchanged.
type legacyPublicationV2 struct {
	Sequence  int64             `json:"sequence,omitempty"`
	Index     int64             `json:"index,omitempty"`
	Topic     string            `json:"topic,omitempty"`
	Resource  string            `json:"resource,omitempty"`
	Agent     string            `json:"agent"`
	Timestamp time.Time         `json:"timestamp"`
	Records   []protocol.Record `json:"records"`
}

func (p legacyPublicationV2) sequence() int64 {
	if p.Sequence != 0 {
		return p.Sequence
	}
	return p.Index
}

func (p legacyPublicationV2) topic() string {
	if p.Topic != "" {
		return p.Topic
	}
	return p.Resource
}

// protocolDocumentV3 is the destination-side protocol.json shape RunV2ToV3
// writes. It exists separately from state's own (unexported) protocolDocument
// so this package does not depend on state's internal wire type.
type protocolDocumentV3 struct {
	Version int `json:"version"`
}

// RunV2ToV3 migrates one v2 root to a distinct new v3 root. The source is
// read-only. Every file copies through byte-for-byte unchanged except:
// protocol.json (rewritten to declare version 3); cursors/<agent>.json (the
// 2026-07-22 cursor field rename); events/<seq>.json, inbox/<agent> (each
// line), and topics/<group>/<topic>/history/<seq>.json (an earlier, also-
// unversioned resource/index -> topic/sequence terminology cleanup found in
// a real production v2 root, alongside the cursor rename, when this
// migration was verified end-to-end against a copy of a live state root);
// and topics/<group>/<topic>/head.json, which carries the same index ->
// sequence rename. Transaction files are never present at this point
// (rejectTransactions already refused the migration if any existed), and
// Lock, Subscription, record-index.json, and state/index.json were
// confirmed unchanged since v1 (via git history across the commit that
// introduced this rename), so they need no translation.
func RunV2ToV3(source, destination string) (Report, error) {
	report := Report{FromVersion: 2, ToVersion: 3}
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return report, errors.New("migration source and destination must be absolute paths")
	}
	source, destination = filepath.Clean(source), filepath.Clean(destination)
	report.Source, report.Destination = source, destination
	if strings.EqualFold(source, destination) {
		return report, errors.New("migration destination must differ from source")
	}
	if err := requireProtocol(source, 2); err != nil {
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
	counts, err := copyV2RootTranslatingLegacyShapes(source, stage)
	if err != nil {
		return report, err
	}
	report.CursorsMigrated = counts.cursors
	report.EventsMigrated = counts.events
	report.PublicationsMigrated = counts.publications
	report.TopicHeadsMigrated = counts.heads
	report.InboxLinesMigrated = counts.inboxLines
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

type v2TranslationCounts struct {
	cursors      int
	events       int
	publications int
	heads        int
	inboxLines   int
}

// copyV2RootTranslatingLegacyShapes recursively copies source into
// destination (an existing empty staging directory), translating every file
// kind documented on RunV2ToV3. Every other file copies through unchanged.
//
// inbox/<agent> files are translated first, in their own pass, before the
// main walk reaches cursors/<agent>.json: each cursor's inbox_offset is a
// byte offset into that same agent's inbox file, and translating the inbox
// file's content changes its byte layout (dropping "key" always shortens a
// line), so the offset must be re-expressed in the new file's terms using
// the per-line length map the pre-pass produces. Processing cursors first
// would leave that offset pointing at the wrong byte position -- not a
// decode error, but a silent replay-or-skip correctness bug.
func copyV2RootTranslatingLegacyShapes(source, destination string) (v2TranslationCounts, error) {
	var counts v2TranslationCounts
	inboxOffsets, err := translateInboxTree(source, destination, &counts)
	if err != nil {
		return counts, err
	}
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			if relative == "." {
				return nil
			}
			return os.MkdirAll(target, 0o700)
		}
		switch {
		case relative == "protocol.json":
			return writeJSON(target, protocolDocumentV3{Version: 3})
		case filepath.Dir(relative) == "cursors" && filepath.Ext(relative) == ".json":
			var legacy legacyCursor
			if err := readJSON(path, &legacy); err != nil {
				return err
			}
			agent := strings.TrimSuffix(filepath.Base(relative), ".json")
			inboxOffset := legacy.InboxOffset
			if lengths, ok := inboxOffsets[agent]; ok {
				inboxOffset = translateInboxOffset(lengths, legacy.InboxOffset)
			}
			counts.cursors++
			return writeJSON(target, state.Cursor{
				Summary:       legacy.Signal,
				SummaryRanges: legacy.SignalRanges,
				InboxOffset:   inboxOffset,
				Topics:        legacy.Resources,
			})
		case filepath.Dir(relative) == "events" && filepath.Ext(relative) == ".json":
			var legacy legacyEvent
			if err := readJSON(path, &legacy); err != nil {
				return err
			}
			summary := legacy.Summary
			if summary == nil {
				summary = legacy.Signal
			}
			if summary == nil {
				return fmt.Errorf("event %s has no signal or summary", relative)
			}
			counts.events++
			return writeJSON(target, state.Event{
				Summary:    translateLegacySummary(*summary),
				Recipients: legacy.Recipients,
			})
		case filepath.Dir(relative) == "inbox" && filepath.Ext(relative) == "":
			// Already translated and written by translateInboxTree above.
			return nil
		case filepath.Base(relative) == "head.json":
			var legacy struct {
				Sequence int64 `json:"sequence,omitempty"`
				Index    int64 `json:"index,omitempty"`
			}
			if err := readJSON(path, &legacy); err != nil {
				return err
			}
			sequence := legacy.Sequence
			if sequence == 0 {
				sequence = legacy.Index
			}
			counts.heads++
			return writeJSON(target, map[string]int64{"sequence": sequence})
		case filepath.Base(filepath.Dir(relative)) == "history" && filepath.Ext(relative) == ".json":
			var legacy legacyPublicationV2
			if err := readJSON(path, &legacy); err != nil {
				return err
			}
			counts.publications++
			return writeJSON(target, protocol.Publication{
				Sequence:  legacy.sequence(),
				Topic:     legacy.topic(),
				Agent:     legacy.Agent,
				Timestamp: legacy.Timestamp,
				Records:   legacy.Records,
			})
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o600)
		}
	})
	return counts, err
}

// translateInboxTree translates and writes every inbox/<agent> file (any
// ".locks" guard-file subdirectory copies through unchanged, untouched
// here) and returns each agent's per-line length map for
// translateInboxOffset. It runs before the main copy walk so cursor
// translation can look up the resulting map by agent name.
func translateInboxTree(source, destination string, counts *v2TranslationCounts) (map[string][]inboxLineLength, error) {
	offsets := map[string][]inboxLineLength{}
	entries, err := os.ReadDir(filepath.Join(source, "inbox"))
	if errors.Is(err, os.ErrNotExist) {
		return offsets, nil
	}
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(destination, "inbox"), 0o700); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, "inbox", entry.Name()))
		if err != nil {
			return nil, err
		}
		rebuilt, lengths, err := translateInboxContent(data)
		if err != nil {
			return nil, fmt.Errorf("inbox file %s: %w", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(destination, "inbox", entry.Name()), rebuilt, 0o600); err != nil {
			return nil, err
		}
		offsets[entry.Name()] = lengths
		counts.inboxLines += len(lengths)
	}
	return offsets, nil
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

// DetectSourceVersion reads a state root's declared protocol version, so a
// caller (the CLI) can choose Run (v1 source) or RunV2ToV3 (v2 source)
// without hardcoding which hop applies.
func DetectSourceVersion(root string) (int, error) {
	var document struct {
		Version int `json:"version"`
	}
	if err := readJSON(filepath.Join(root, "protocol.json"), &document); err != nil {
		return 0, err
	}
	return document.Version, nil
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

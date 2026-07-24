package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/state"
	"github.com/danlavee/Conductor/protocol"
)

// legacyCursor is Cursor's v2 wire shape, before the v3 rename that shipped
// without a protocol bump: signal_index -> summary_sequence,
// resource_indexes -> topic_sequences, signal_ranges -> summary_ranges.
// inbox_offset is unchanged. It is decoded with plain encoding/json (not the
// strict, current state.Cursor), since old-shape files predate the new
// field names and would be rejected by DisallowUnknownFields.
type legacyCursor struct {
	Signal        int64               `json:"signal_index"`
	SignalRanges  []state.IndexRange  `json:"signal_ranges,omitempty"`
	Resources     map[string]int64    `json:"resource_indexes"`
	Summary       *int64              `json:"summary_sequence"`
	SummaryRanges *[]state.IndexRange `json:"summary_ranges"`
	Topics        *map[string]int64   `json:"topic_sequences"`
	InboxOffset   int64               `json:"inbox_offset"`
}

func (c legacyCursor) summary() int64 {
	if c.Summary != nil {
		return *c.Summary
	}
	return c.Signal
}

func (c legacyCursor) summaryRanges() []state.IndexRange {
	if c.SummaryRanges != nil {
		return *c.SummaryRanges
	}
	return c.SignalRanges
}

func (c legacyCursor) topics() map[string]int64 {
	if c.Topics != nil {
		return *c.Topics
	}
	return c.Resources
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

// inboxLineLength tracks source and translated bytes, including the newline.
// Cursor offsets must be remapped with the content to prevent replay or skips.
type inboxLineLength struct {
	sourceBytes     int64
	translatedBytes int64
}

type inboxTranslation struct {
	lengths    []inboxLineLength
	sourceSize int64
}

// translateInboxContent rewrites an inbox/<agent> file: a newline-delimited
// (JSONL) log of compact Summary values, one per unread delivery, appended
// to by writeEvent for every recipient of a signal. Each line decodes as
// legacySummary and re-encodes as the current, compact Summary shape,
// preserving line order and count. It returns the rebuilt file content and
// each line's source and translated lengths for translateInboxOffset.
func translateInboxContent(data []byte) ([]byte, []inboxLineLength, error) {
	var rebuilt strings.Builder
	var lengths []inboxLineLength
	completeSize := bytes.LastIndexByte(data, '\n') + 1
	for _, rawLine := range bytes.SplitAfter(data[:completeSize], []byte{'\n'}) {
		if len(rawLine) == 0 {
			continue
		}
		line := string(rawLine[:len(rawLine)-1])
		if strings.TrimSpace(line) == "" {
			rebuilt.Write(rawLine)
			length := int64(len(rawLine))
			lengths = append(lengths, inboxLineLength{sourceBytes: length, translatedBytes: length})
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
		lengths = append(lengths, inboxLineLength{
			sourceBytes:     int64(len(line)) + 1,
			translatedBytes: int64(len(encoded)) + 1,
		})
	}
	rebuilt.Write(data[completeSize:])
	return []byte(rebuilt.String()), lengths, nil
}

// translateInboxOffset preserves consumed complete lines, whose offsets always
// fall on line boundaries, plus any position within an incomplete tail. An
// offset beyond the source file resets to zero.
func translateInboxOffset(lengths []inboxLineLength, sourceOffset, sourceSize int64) int64 {
	if sourceOffset > sourceSize {
		return 0
	}
	var sourceConsumed, translatedConsumed int64
	for _, length := range lengths {
		if sourceConsumed >= sourceOffset {
			break
		}
		sourceConsumed += length.sourceBytes
		translatedConsumed += length.translatedBytes
	}
	if sourceOffset > sourceConsumed {
		return translatedConsumed + sourceOffset - sourceConsumed
	}
	return translatedConsumed
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

// protocolDocumentV3 is the destination-side protocol.json shape runV2ToV3
// writes. It exists separately from state's own (unexported) protocolDocument
// so this package does not depend on state's internal wire type.
type protocolDocumentV3 struct {
	Version int `json:"version"`
}

// runV2ToV3 migrates one v2 root to a distinct new v3 root. The source is
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
func runV2ToV3(source, destination string) (report Report, resultErr error) {
	preparation, err := prepareMigration(source, destination, 2, 3)
	report = preparation.report
	if err != nil {
		return report, err
	}
	defer cleanupStage(preparation.stage, &resultErr)

	counts, err := copyV2RootTranslatingLegacyShapes(preparation.source, preparation.stage)
	if err != nil {
		return report, err
	}
	report.CursorsMigrated = counts.cursors
	report.EventsMigrated = counts.events
	report.PublicationsMigrated = counts.publications
	report.TopicHeadsMigrated = counts.heads
	report.InboxLinesMigrated = counts.inboxLines
	if err := publishStage(preparation.stage, preparation.destination, preparation.destinationExists); err != nil {
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
// kind documented on runV2ToV3. Every other file copies through unchanged.
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
		parts := strings.Split(relative, string(os.PathSeparator))
		isTopicHead := len(parts) == 4 && parts[0] == "topics" && parts[3] == "head.json"
		isTopicHistory := len(parts) == 5 && parts[0] == "topics" && parts[3] == "history" && filepath.Ext(parts[4]) == ".json"
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
			if translation, ok := inboxOffsets[agent]; ok {
				inboxOffset = translateInboxOffset(translation.lengths, legacy.InboxOffset, translation.sourceSize)
			}
			counts.cursors++
			return writeJSON(target, state.Cursor{
				Summary:       legacy.summary(),
				SummaryRanges: legacy.summaryRanges(),
				InboxOffset:   inboxOffset,
				Topics:        legacy.topics(),
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
		case filepath.Dir(relative) == "inbox":
			// Already translated and written by translateInboxTree above.
			return nil
		case isTopicHead:
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
		case isTopicHistory:
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
func translateInboxTree(source, destination string, counts *v2TranslationCounts) (map[string]inboxTranslation, error) {
	offsets := map[string]inboxTranslation{}
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
		offsets[entry.Name()] = inboxTranslation{lengths: lengths, sourceSize: int64(len(data))}
		counts.inboxLines += len(lengths)
	}
	return offsets, nil
}

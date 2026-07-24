// Package migrate performs explicit, non-destructive state protocol migrations.
package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const protocolVersion4 = 4

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
	// The fields below count v2 files translated to their v3 equivalents.
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

type migrationPreparation struct {
	report            Report
	source            string
	destination       string
	stage             string
	destinationExists bool
}

// Run detects the source protocol and migrates it through the supported hop.
func Run(source, destination string) (Report, error) {
	version, err := detectSourceVersion(source)
	if err != nil {
		return Report{}, err
	}
	switch version {
	case 1:
		return runV1ToV4(source, destination)
	case 2:
		return runV2ToV3(source, destination)
	case 3:
		return runV3ToV4(source, destination)
	default:
		return Report{}, fmt.Errorf("migrate supports v1, v2 or v3 source roots, found protocol %d", version)
	}
}

func prepareMigration(source, destination string, fromVersion, toVersion int) (migrationPreparation, error) {
	preparation := migrationPreparation{
		report: Report{FromVersion: fromVersion, ToVersion: toVersion},
	}
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return preparation, errors.New("migration source and destination must be absolute paths")
	}

	preparation.source = filepath.Clean(source)
	preparation.destination = filepath.Clean(destination)
	preparation.report.Source = preparation.source
	preparation.report.Destination = preparation.destination
	if strings.EqualFold(preparation.source, preparation.destination) {
		return preparation, errors.New("migration destination must differ from source")
	}
	if err := requireProtocol(preparation.source, fromVersion); err != nil {
		return preparation, err
	}
	if err := rejectTransactions(preparation.source); err != nil {
		return preparation, err
	}

	destinationExists, err := requireEmptyDestination(preparation.destination)
	if err != nil {
		return preparation, err
	}
	preparation.destinationExists = destinationExists

	parent := filepath.Dir(preparation.destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return preparation, err
	}
	preparation.stage, err = os.MkdirTemp(parent, ".conductor-migrate-*")
	if err != nil {
		return preparation, err
	}
	return preparation, nil
}

func initializeV4Root(root string) error {
	if err := writeJSON(filepath.Join(root, "protocol.json"), map[string]int{"version": protocolVersion4}); err != nil {
		return err
	}
	for _, dir := range []string{"registry", "topics", "subscriptions", "locks", "inbox", "events", "cursors", "transactions", "state"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func cleanupStage(stage string, resultErr *error) {
	if err := os.RemoveAll(stage); err != nil {
		*resultErr = errors.Join(*resultErr, fmt.Errorf("remove migration staging: %w", err))
	}
}

func publishStage(stage, destination string, destinationExists bool) error {
	return publishStageWith(stage, destination, destinationExists, os.Rename, os.RemoveAll)
}

type committedMigrationError struct {
	err error
}

func (e *committedMigrationError) Error() string {
	return "migration committed but cleanup failed: " + e.err.Error()
}

func (e *committedMigrationError) Unwrap() error {
	return e.err
}

func publishStageWith(
	stage, destination string,
	destinationExists bool,
	rename func(string, string) error,
	removeAll func(string) error,
) error {
	backupRoot := ""
	backupDestination := ""
	if destinationExists {
		var err error
		backupRoot, err = os.MkdirTemp(filepath.Dir(destination), ".conductor-destination-*")
		if err != nil {
			return err
		}
		backupDestination = filepath.Join(backupRoot, "original")
		if err := rename(destination, backupDestination); err != nil {
			if cleanupErr := removeAll(backupRoot); cleanupErr != nil {
				return errors.Join(err, fmt.Errorf("remove migration destination backup: %w", cleanupErr))
			}
			return err
		}
	}
	if err := rename(stage, destination); err != nil {
		if destinationExists {
			if restoreErr := rename(backupDestination, destination); restoreErr != nil {
				return errors.Join(err, fmt.Errorf("restore empty migration destination: %w", restoreErr))
			}
			if cleanupErr := removeAll(backupRoot); cleanupErr != nil {
				return errors.Join(err, fmt.Errorf("remove migration destination backup: %w", cleanupErr))
			}
		}
		return err
	}
	if destinationExists {
		if err := removeAll(backupRoot); err != nil {
			return &committedMigrationError{err: fmt.Errorf("remove migration destination backup: %w", err)}
		}
	}
	return nil
}

func detectSourceVersion(root string) (int, error) {
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

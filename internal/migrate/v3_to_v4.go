package migrate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/danlavee/Conductor/protocol"
)

// runV3ToV4 migrates one v3 root to a distinct new v4 root. The source is read-only.
func runV3ToV4(source, destination string) (report Report, resultErr error) {
	preparation, err := prepareMigration(source, destination, 3, protocolVersion4)
	report = preparation.report
	if err != nil {
		return report, err
	}
	defer cleanupStage(preparation.stage, &resultErr)

	if err := initializeV4Root(preparation.stage); err != nil {
		return report, err
	}
	if err := copyV3State(preparation.source, preparation.stage); err != nil {
		return report, err
	}
	report.Topics, report.Records, err = migrateV3Topics(preparation.source, preparation.stage)
	if err != nil {
		return report, err
	}
	if err := publishStage(preparation.stage, preparation.destination, preparation.destinationExists); err != nil {
		return report, err
	}
	return report, nil
}

func copyV3State(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}
		if relativePath == "protocol.json" {
			return nil
		}
		if relativePath == "topics" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		destinationPath := filepath.Join(destination, relativePath)
		if info.IsDir() {
			return os.MkdirAll(destinationPath, 0o700)
		}
		return copyFile(path, destinationPath)
	})
}

func migrateV3Topics(source, destination string) (topicCount, recordCount int, err error) {
	groups, err := os.ReadDir(filepath.Join(source, "topics"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}

	for _, group := range groups {
		if !group.IsDir() {
			continue
		}
		groupPath := filepath.Join(source, "topics", group.Name())
		topics, err := os.ReadDir(groupPath)
		if err != nil {
			return 0, 0, err
		}

		for _, topic := range topics {
			if !topic.IsDir() {
				continue
			}
			sourceTopicDir := filepath.Join(groupPath, topic.Name())
			destinationTopicDir := filepath.Join(destination, "topics", group.Name(), topic.Name())
			if err := os.MkdirAll(destinationTopicDir, 0o700); err != nil {
				return 0, 0, err
			}

			sourceRecordIndex := filepath.Join(sourceTopicDir, "record-index.json")
			if _, err := os.Stat(sourceRecordIndex); err == nil {
				if err := copyFile(sourceRecordIndex, filepath.Join(destinationTopicDir, "record-index.json")); err != nil {
					return 0, 0, err
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return 0, 0, err
			}

			migratedRecords, err := migrateV3History(
				filepath.Join(sourceTopicDir, "history"),
				filepath.Join(destinationTopicDir, "history.jsonl"),
			)
			if err != nil {
				return 0, 0, err
			}
			recordCount += migratedRecords
			topicCount++
		}
	}
	return topicCount, recordCount, nil
}

func migrateV3History(sourceDirectory, destinationPath string) (recordCount int, resultErr error) {
	entries, err := os.ReadDir(sourceDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	var historyFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			historyFiles = append(historyFiles, entry.Name())
		}
	}
	sort.Strings(historyFiles)
	if len(historyFiles) == 0 {
		return 0, nil
	}

	destinationHistory, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, destinationHistory.Close())
	}()

	encoder := json.NewEncoder(destinationHistory)
	for _, historyFile := range historyFiles {
		content, err := os.ReadFile(filepath.Join(sourceDirectory, historyFile))
		if err != nil {
			return 0, err
		}
		var publication protocol.Publication
		if err := json.Unmarshal(content, &publication); err != nil {
			return 0, err
		}
		if err := encoder.Encode(publication); err != nil {
			return 0, err
		}
		recordCount += len(publication.Records)
	}
	return recordCount, nil
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o600)
}

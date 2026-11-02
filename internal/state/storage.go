package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

var portableName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".conductor-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return platform.ReplaceFile(tempPath, path)
}

func appendJSONL(path string, value any) error {
	line, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := trimUnterminatedTail(f); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func removeEventually(path string) error {
	var err error
	for attempt := 0; attempt < 100; attempt++ {
		err = os.Remove(path)
		if err == nil {
			return platform.SyncParent(path)
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if !errors.Is(err, os.ErrPermission) {
			return err
		}
		time.Sleep(2 * time.Millisecond)
	}
	return err
}

func readJSON(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReader(file))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON file contains multiple values")
		}
		return err
	}
	switch typed := value.(type) {
	case *Agent:
		return validateAgent(typed)
	case *Publication:
		return validatePublication(typed)
	case *Summary:
		return validateSummary(typed)
	default:
		if validator, ok := value.(stateValidator); ok {
			return validator.validate()
		}
	}
	return nil
}

// WriteJSON writes indented JSON followed by a newline.
func WriteJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func validName(value string) error {
	if len(value) > 64 || !portableName.MatchString(value) || windowsReservedName(value) {
		return fmt.Errorf("invalid name %q", value)
	}
	return nil
}

func windowsReservedName(value string) bool {
	base := strings.SplitN(value, ".", 2)[0]
	if base == "con" || base == "prn" || base == "aux" || base == "nul" {
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

func validTopic(topic string) error {
	parts := strings.Split(topic, "/")
	if len(parts) != 2 {
		return fmt.Errorf("topic must be <group>/<topic>: %q", topic)
	}
	if err := validName(parts[0]); err != nil {
		return err
	}
	return validName(parts[1])
}

func indexName(index int64) string { return fmt.Sprintf("%020d.json", index) }

func sortedRecords(values map[int64]Record) []Record {
	indexes := make([]int64, 0, len(values))
	for index := range values {
		indexes = append(indexes, index)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	records := make([]Record, 0, len(indexes))
	for _, index := range indexes {
		records = append(records, values[index])
	}
	return records
}

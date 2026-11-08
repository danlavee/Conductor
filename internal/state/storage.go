package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/danlavee/Conductor/internal/platform"
)

var portableName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return platform.WriteFile(path, append(data, '\n'))
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

// trimUnterminatedTail drops a crash-torn final line from a newline-delimited
// file. It does not move the file offset; an appender seeks to the end itself.
func trimUnterminatedTail(file *os.File) error {
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}

	var size int64
	buf := make([]byte, 4096)
	offset := info.Size()
	for offset > 0 {
		chunkSize := int64(len(buf))
		if offset < chunkSize {
			chunkSize = offset
		}
		offset -= chunkSize
		if _, err := file.ReadAt(buf[:chunkSize], offset); err != nil {
			return err
		}
		if index := bytes.LastIndexByte(buf[:chunkSize], '\n'); index != -1 {
			size = offset + int64(index) + 1
			break
		}
	}
	return file.Truncate(size)
}

func readJSON(path string, value any) error {
	file, err := platform.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReader(file))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
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

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	conductor "github.com/danlavee/Conductor/internal/state"
)

const (
	// filePrefix substitutes for any text operand. A body passed as a shell
	// argument is mangled silently by quoting; a path is not.
	filePrefix = "--file="

	// jsonlPrefix bulk-loads many records where filePrefix loads one.
	jsonlPrefix = "--jsonl="
)

// resolveText returns the text an operand denotes: itself, or the contents of
// the file it names, verbatim but for the trailing newline every editor adds.
func resolveText(operand string) (string, error) {
	path, ok := strings.CutPrefix(operand, filePrefix)
	if !ok {
		return operand, nil
	}
	if path == "" {
		return "", usageError()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := strings.TrimRight(string(data), "\r\n")
	if text == "" {
		return "", fmt.Errorf("%s: no text to publish", path)
	}
	return text, nil
}

// putJSONL loads a JSON string per line as one record each, in one
// publication.
func putJSONL(client *conductor.Client, topic, path string) (conductor.Dispatch, error) {
	file, err := os.Open(path)
	if err != nil {
		return conductor.Dispatch{}, err
	}
	defer file.Close()
	var ops []conductor.Op
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var text string
		if err := json.Unmarshal([]byte(line), &text); err != nil {
			return conductor.Dispatch{}, fmt.Errorf("%s: line is not a JSON string: %w", path, err)
		}
		ops = append(ops, conductor.Op{Text: text})
	}
	if err := scanner.Err(); err != nil {
		return conductor.Dispatch{}, err
	}
	if len(ops) == 0 {
		return conductor.Dispatch{}, fmt.Errorf("%s: no non-blank lines to load", path)
	}
	return client.Publish(topic, ops)
}

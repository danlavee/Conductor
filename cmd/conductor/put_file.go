package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	conductor "github.com/danlavee/Conductor"
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

// putJSONL loads a JSON string per line as one record each, in one atomic
// transaction.
func putJSONL(client *conductor.Client, topic, path string) (conductor.Publication, error) {
	file, err := os.Open(path)
	if err != nil {
		return conductor.Publication{}, err
	}
	defer file.Close()
	if err := client.Begin(topic); err != nil {
		return conductor.Publication{}, err
	}
	staged := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var text string
		if err := json.Unmarshal([]byte(line), &text); err != nil {
			return conductor.Publication{}, abortWith(client, fmt.Errorf("%s: line is not a JSON string: %w", path, err))
		}
		if _, err := client.StagePut(text); err != nil {
			return conductor.Publication{}, abortWith(client, err)
		}
		staged++
	}
	if err := scanner.Err(); err != nil {
		return conductor.Publication{}, abortWith(client, err)
	}
	if staged == 0 {
		return conductor.Publication{}, abortWith(client, fmt.Errorf("%s: no non-blank lines to load", path))
	}
	return client.Commit()
}

func abortWith(client *conductor.Client, cause error) error {
	if abortErr := client.Abort(); abortErr != nil {
		return errors.Join(cause, fmt.Errorf("abort failed: %w", abortErr))
	}
	return cause
}

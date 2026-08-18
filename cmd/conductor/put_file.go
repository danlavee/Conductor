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

// putFile bulk-loads a JSONL file into one atomic transaction.
func putFile(client *conductor.Client, topic, path string) (conductor.Publication, error) {
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

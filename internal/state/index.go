package state

import (
	"errors"
	"math"
	"os"
	"path/filepath"
)

func (c *Client) nextIndex() (int64, error) {
	guard := filepath.Join(c.Home, "state", "index.guard")
	release, err := c.acquireStateMutex(guard)
	if err != nil {
		return 0, err
	}
	defer release()
	path := filepath.Join(c.Home, "state", "index.json")
	var state struct {
		Index int64 `json:"index"`
	}
	if err := readJSON(path, &state); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	if state.Index < 0 {
		return 0, errors.New("invalid global index state")
	}
	if state.Index == math.MaxInt64 {
		return 0, errors.New("global sequence is exhausted")
	}
	state.Index++
	if err := writeJSONAtomic(path, state); err != nil {
		return 0, err
	}
	return state.Index, nil
}

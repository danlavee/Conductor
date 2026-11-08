package state

import (
	"errors"
	"os"
	"path/filepath"
)

func (c *Client) loadCursor(agent string) (Cursor, error) {
	cursor := Cursor{Topics: map[string]int64{}}
	err := readJSON(filepath.Join(c.Home, "cursors", agent+".json"), &cursor)
	if errors.Is(err, os.ErrNotExist) {
		return cursor, nil
	}
	if cursor.Topics == nil {
		cursor.Topics = map[string]int64{}
	}
	return cursor, err
}

func (c *Client) saveCursor(agent string, cursor Cursor) error {
	if c.saveCursorFn != nil {
		return c.saveCursorFn(agent, cursor)
	}
	return writeJSONAtomic(filepath.Join(c.Home, "cursors", agent+".json"), cursor)
}

func (c *Client) updateCursor(agent string, update func(*Cursor)) error {
	release, err := c.acquireStateMutex(filepath.Join(c.Home, "cursors", agent+".guard"))
	if err != nil {
		return err
	}
	cursor, err := c.loadCursor(agent)
	if err != nil {
		_ = release()
		return err
	}
	update(&cursor)
	if err := c.saveCursor(agent, cursor); err != nil {
		_ = release()
		return err
	}
	return release()
}

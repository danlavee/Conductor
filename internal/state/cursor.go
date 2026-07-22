package state

import (
	"errors"
	"os"
	"path/filepath"
)

func (c *Client) loadCursor(agent string) (Cursor, error) {
	cursor := Cursor{Resources: map[string]int64{}}
	err := readJSON(filepath.Join(c.Home, "cursors", agent+".json"), &cursor)
	if errors.Is(err, os.ErrNotExist) {
		return cursor, nil
	}
	if cursor.Resources == nil {
		cursor.Resources = map[string]int64{}
	}
	if len(cursor.SignalRanges) == 0 && cursor.Signal > 0 {
		cursor.SignalRanges = []IndexRange{{From: 1, To: cursor.Signal}}
	}
	return cursor, err
}

func (c *Client) saveCursor(agent string, cursor Cursor) error {
	return writeJSONAtomic(filepath.Join(c.Home, "cursors", agent+".json"), cursor)
}

func (c *Client) updateCursor(agent string, update func(*Cursor)) error {
	release, err := c.acquireLeaseGuard(filepath.Join(c.Home, "cursors", agent+".guard"))
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

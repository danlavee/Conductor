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
	if len(cursor.SummaryRanges) == 0 && cursor.Summary > 0 {
		cursor.SummaryRanges = []IndexRange{{From: 1, To: cursor.Summary}}
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

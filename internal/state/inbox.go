package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func (c *Client) appendInbox(agent string, line []byte) error {
	guard := filepath.Join(c.Home, "inbox", ".locks", agent+".guard")
	release, err := c.acquireLeaseGuard(guard)
	if err != nil {
		return err
	}
	path := filepath.Join(c.Home, "inbox", agent)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		_ = release()
		return err
	}
	if err = trimUnterminatedInboxTail(file); err == nil {
		if _, err = file.Seek(0, io.SeekEnd); err == nil {
			var written int
			written, err = file.Write(line)
			if err == nil && written != len(line) {
				err = io.ErrShortWrite
			}
		}
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if releaseErr := release(); err == nil {
		err = releaseErr
	}
	return err
}

// pendingInboxSummaries skips malformed complete lines and leaves an
// unterminated crash tail unconsumed for appendInbox to repair.
func (c *Client) pendingInboxSummaries(agent string, cursor Cursor, since int64) ([]Summary, int64, error) {
	file, err := os.Open(filepath.Join(c.Home, "inbox", agent))
	if errors.Is(err, os.ErrNotExist) {
		return nil, cursor.InboxOffset, nil
	}
	if err != nil {
		return nil, cursor.InboxOffset, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, cursor.InboxOffset, err
	}
	offset := cursor.InboxOffset
	if offset > info.Size() {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	reader := bufio.NewReader(file)
	var pending []Summary
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			return pending, offset, nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return pending, offset, readErr
		}
		if line[len(line)-1] != '\n' {
			return pending, offset, nil
		}
		offset += int64(len(line))
		var summary Summary
		if err := json.Unmarshal(line, &summary); err != nil {
			continue
		}
		if err := validateSummary(&summary); err != nil {
			continue
		}
		if summary.Sequence > since && !indexAcknowledged(cursor.SummaryRanges, summary.Sequence) {
			pending = append(pending, summary)
		}
	}
}

func (c *Client) inboxOffsetThrough(agent string, sequence int64) (int64, error) {
	cursor, err := c.loadCursor(agent)
	if err != nil {
		return 0, err
	}
	file, err := os.Open(filepath.Join(c.Home, "inbox", agent))
	if errors.Is(err, os.ErrNotExist) {
		return cursor.InboxOffset, nil
	}
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	offset := cursor.InboxOffset
	if offset > info.Size() {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			return offset, nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return 0, readErr
		}
		if line[len(line)-1] != '\n' {
			return offset, nil
		}
		offset += int64(len(line))
		var candidate Summary
		if err := json.Unmarshal(line, &candidate); err != nil {
			continue
		}
		if candidate.Sequence == sequence {
			return offset, nil
		}
	}
}

func trimUnterminatedInboxTail(file *os.File) error {
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
	if err := file.Truncate(size); err != nil {
		return err
	}
	_, err = file.Seek(0, io.SeekEnd)
	return err
}

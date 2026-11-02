package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// CurrentProtocolVersion is the one on-disk state protocol this binary supports.
const CurrentProtocolVersion = 5

const protocolFileName = "protocol.json"

type protocolDocument struct {
	Version *int `json:"version"`
}

// initializeProtocol declares this protocol on a root that has no declaration
// and checks the one it finds otherwise. The declaration is hard-linked into
// place from a complete temporary file, so no reader sees a partial one and
// racing initializers settle on whichever link landed first.
func initializeProtocol(home string) error {
	if err := checkProtocol(home); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(home, ".protocol-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = fmt.Fprintf(temp, "{\n  \"version\": %d\n}\n", CurrentProtocolVersion)
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Link(temp.Name(), protocolPath(home)); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return checkProtocol(home)
}

func (c *Client) validateProtocol() error {
	err := checkProtocol(c.Home)
	if errors.Is(err, fs.ErrNotExist) {
		return protocolMismatch("state protocol declaration is missing", nil)
	}
	return err
}

// checkProtocol returns fs.ErrNotExist when the root declares nothing yet.
func checkProtocol(home string) error {
	data, err := os.ReadFile(protocolPath(home))
	if err != nil {
		return err
	}
	var document protocolDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || decoder.More() {
		return protocolMismatch("state protocol declaration is malformed", nil)
	}
	if document.Version == nil {
		return protocolMismatch("state protocol declaration has no version", nil)
	}
	if *document.Version != CurrentProtocolVersion {
		return protocolMismatch("state protocol is unsupported", document.Version)
	}
	return nil
}

func protocolMismatch(message string, found *int) *ProtocolError {
	detail := &ProtocolVersionDetail{Supported: CurrentProtocolVersion}
	if found != nil {
		value := *found
		detail.Found = &value
	}
	return &ProtocolError{Code: "PROTOCOL_MISMATCH", Text: message, Protocol: detail}
}

func protocolPath(home string) string {
	return filepath.Join(home, protocolFileName)
}

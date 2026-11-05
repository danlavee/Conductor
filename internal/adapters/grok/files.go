package grok

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/platform"
	"github.com/danlavee/Conductor/internal/state"
)

const (
	AdapterName        = "grok"
	SessionEnvironment = "GROK_SESSION_ID"
	SessionPlaceholder = "${GROK_SESSION_ID}"
	PollInterval       = 200 * time.Millisecond
)

// Presentation is one batch the resident is holding for a session to accept.
type Presentation struct {
	Session    string                `json:"session"`
	OwnerPID   int                   `json:"owner_pid"`
	OwnerStart string                `json:"owner_start,omitempty"`
	Claimant   string                `json:"claimant,omitempty"`
	Token      string                `json:"token"`
	Receipt    state.DeliveryReceipt `json:"receipt"`
}

type residency struct {
	Session  string `json:"session"`
	PID      int    `json:"pid"`
	Start    string `json:"start,omitempty"`
	Released bool   `json:"released"`
}

func presentationPath(root, agent string) (string, error) {
	return filepath.Join(binding.Directory(root, AdapterName), "presentations", agent+".json"), nil
}

func residencyPath(root, agent string) string {
	return filepath.Join(binding.Directory(root, AdapterName), "residency", agent+".json")
}

func flagPath(root, kind, session string) string {
	return filepath.Join(binding.Directory(root, AdapterName), "flags", kind+"-"+session)
}

func loadPresentation(root, agent string) (*Presentation, error) {
	var shown Presentation
	err := readJSON(mustPresentationPath(root, agent), &shown)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &shown, nil
}

func savePresentation(root, agent string, shown Presentation) error {
	return writeJSON(mustPresentationPath(root, agent), shown)
}

func deletePresentation(root, agent string) error {
	err := os.Remove(mustPresentationPath(root, agent))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func mustPresentationPath(root, agent string) string {
	path, _ := presentationPath(root, agent)
	return path
}

func loadResidency(root, agent string) (*residency, error) {
	var live residency
	err := readJSON(residencyPath(root, agent), &live)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &live, nil
}

func saveResidency(root, agent string, live residency) error {
	return writeJSON(residencyPath(root, agent), live)
}

// ResidentAlive reports a live resident for this session that has not been released.
func ResidentAlive(root, agent, session string) bool {
	live, err := loadResidency(root, agent)
	if err != nil || live == nil || live.Released || live.Session != session {
		return false
	}
	return processMatches(live.PID, live.Start)
}

// ResidentReleased reports that teardown has asked the resident to exit.
func ResidentReleased(root, agent, session string) bool {
	live, err := loadResidency(root, agent)
	return err == nil && live != nil && live.Released && live.Session == session
}

func processMatches(pid int, start string) bool {
	if pid <= 0 || !platform.ProcessAlive(pid) {
		return false
	}
	if start == "" {
		return true
	}
	current, ok := platform.ProcessStartToken(pid)
	return ok && current == start
}

func setFlag(root, kind, session string) error {
	return writeJSON(flagPath(root, kind, session), []byte("1"))
}

func flagSet(root, kind, session string) (bool, error) {
	_, err := os.Stat(flagPath(root, kind, session))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func clearFlag(root, kind, session string) error {
	err := os.Remove(flagPath(root, kind, session))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Reannounce asks the next end of turn to repeat this session's identity.
func Reannounce(root, session string) error {
	return setFlag(root, "reannounce", session)
}

// Release marks the resident free to exit and forgets the session binding.
// A subagent teardown must not forget the parent session.
func Release(root, session string, subagent bool) error {
	if subagent {
		return nil
	}
	agent, err := binding.Identity(root, AdapterName, session)
	if err != nil {
		return err
	}
	if agent != "" {
		if live, err := loadResidency(root, agent); err != nil {
			return err
		} else if live != nil && live.Session == session {
			live.Released = true
			if err := saveResidency(root, agent, *live); err != nil {
				return err
			}
		}
	}
	return binding.Forget(root, AdapterName, session)
}

func receiptToken(receipt state.DeliveryReceipt) (string, error) {
	body, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8]), nil
}

func ownerAlive(shown Presentation) bool {
	if shown.OwnerPID <= 0 {
		return true
	}
	return processMatches(shown.OwnerPID, shown.OwnerStart)
}

func readJSON(path string, dest any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

func writeJSON(path string, value any) error {
	var data []byte
	var err error
	switch typed := value.(type) {
	case []byte:
		data = typed
	default:
		data, err = json.Marshal(value)
		if err != nil {
			return err
		}
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".conductor-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return platform.ReplaceFile(tempPath, path)
}

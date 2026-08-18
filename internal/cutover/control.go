// Package cutover owns the protocol-independent coordination plane used while
// replacing a Conductor state root and its executable.
package cutover

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

const Capability = 1

type Phase string

const (
	Active   Phase = "active"
	Freezing Phase = "freezing"
	Frozen   Phase = "frozen"
	Replaced Phase = "replaced"
)

type State struct {
	Phase         Phase     `json:"phase"`
	CutoverID     string    `json:"cutover_id,omitempty"`
	Release       string    `json:"release,omitempty"`
	LastCutoverID string    `json:"last_cutover_id,omitempty"`
	LastRelease   string    `json:"last_release,omitempty"`
	Generation    int64     `json:"generation"`
	Capability    int       `json:"capability"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Activation struct {
	Type       string `json:"type"`
	CutoverID  string `json:"cutover_id"`
	Release    string `json:"release"`
	Generation int64  `json:"generation"`
}

type BlockedError struct {
	State State
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("conductor state operations are blocked during cutover %q (%s)", e.State.CutoverID, e.State.Phase)
}

var leaseSequence atomic.Uint64

// Directory returns the control-plane location for root. It is a sibling of
// root, so replacing root cannot remove it or invalidate an open control lock.
func Directory(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	identity := absolute
	if runtime.GOOS == "windows" {
		identity = strings.ToLower(identity)
	}
	sum := sha256.Sum256([]byte(identity))
	name := "." + filepath.Base(absolute) + "-cutover-" + hex.EncodeToString(sum[:16])
	return filepath.Join(filepath.Dir(absolute), name), nil
}

func observe(root string) (State, bool, error) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, exists, err := observeOnce(root)
		if !platform.IsTransientFileAccess(err) || time.Now().After(deadline) {
			return state, exists, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func observeOnce(root string) (State, bool, error) {
	dir, err := Directory(root)
	if err != nil {
		return State{}, false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		if _, markerErr := os.Stat(filepath.Join(dir, "initialized")); markerErr == nil {
			return State{}, true, errors.New("cutover state is missing")
		} else if !errors.Is(markerErr, os.ErrNotExist) {
			return State{}, true, markerErr
		}
		return activeState(0), false, nil
	}
	if err != nil {
		return State{}, true, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, true, fmt.Errorf("cutover state is corrupt: %w", err)
	}
	if err := validate(state); err != nil {
		return State{}, true, err
	}
	return state, true, nil
}

func Observe(root string) (State, bool, error) {
	return observe(root)
}

func activeState(generation int64) State {
	return State{Phase: Active, Generation: generation, Capability: Capability}
}

func validate(state State) error {
	if state.Capability != Capability {
		return fmt.Errorf("unsupported cutover capability %d", state.Capability)
	}
	switch state.Phase {
	case Active:
		return nil
	case Freezing, Frozen, Replaced:
		if strings.TrimSpace(state.CutoverID) == "" || strings.TrimSpace(state.Release) == "" {
			return errors.New("cutover state is missing identity or release")
		}
		return nil
	default:
		return fmt.Errorf("invalid cutover phase %q", state.Phase)
	}
}

// Admit creates a crash-released operation lease while admission is active.
// Freeze closes admission under the same gate before enumerating these leases.
func Admit(root string) (func() error, error) {
	dir, err := Directory(root)
	if err != nil {
		return nil, err
	}
	releaseGate, err := platform.AcquireFileMutex(filepath.Join(dir, "gate.lock"), 3*time.Minute)
	if err != nil {
		return nil, err
	}
	state, exists, stateErr := observe(root)
	if stateErr != nil {
		_ = releaseGate()
		return nil, stateErr
	}
	if state.Phase != Active {
		_ = releaseGate()
		return nil, &BlockedError{State: state}
	}
	if !exists {
		state.UpdatedAt = time.Now().UTC()
		if err := writeState(dir, state); err != nil {
			_ = releaseGate()
			return nil, err
		}
	}
	leasePath := filepath.Join(dir, "leases", fmt.Sprintf("%d-%d.lease", os.Getpid(), leaseSequence.Add(1)))
	releaseLease, err := platform.AcquireFileMutex(leasePath, 0)
	if err != nil {
		_ = releaseGate()
		return nil, err
	}
	if err := releaseGate(); err != nil {
		_ = releaseLease()
		return nil, err
	}
	return func() error {
		releaseErr := releaseLease()
		removeErr := removeLease(leasePath)
		return errors.Join(releaseErr, removeErr)
	}, nil
}

// Freeze closes admission, waits for every admitted operation, then completes
// the barrier. validateRoot runs after the drain while no normal operation can
// touch root. A validation failure safely returns the control plane to active.
func Freeze(root, id, release string, validateRoot func() error) (State, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(release) == "" {
		return State{}, errors.New("cutover id and release are required")
	}
	dir, err := Directory(root)
	if err != nil {
		return State{}, err
	}
	releaseGate, err := platform.AcquireFileMutex(filepath.Join(dir, "gate.lock"), 3*time.Minute)
	if err != nil {
		return State{}, err
	}
	current, _, err := observe(root)
	if err != nil {
		_ = releaseGate()
		return State{}, err
	}
	if current.Phase != Active {
		_ = releaseGate()
		return State{}, fmt.Errorf("freeze requires active cutover state, found %s", current.Phase)
	}
	freezing := State{
		Phase: Freezing, CutoverID: id, Release: release,
		Generation: current.Generation, Capability: Capability, UpdatedAt: time.Now().UTC(),
	}
	if err := writeState(dir, freezing); err != nil {
		_ = releaseGate()
		return State{}, err
	}
	leases, err := leasePaths(dir)
	if releaseErr := releaseGate(); err == nil {
		err = releaseErr
	}
	if err != nil {
		return State{}, err
	}
	for _, lease := range leases {
		releaseLease, acquireErr := platform.AcquireFileMutex(lease, 3*time.Minute)
		if acquireErr != nil {
			return State{}, fmt.Errorf("drain operation lease %s: %w", filepath.Base(lease), acquireErr)
		}
		_ = releaseLease()
		_ = removeLease(lease)
	}
	releaseGate, err = platform.AcquireFileMutex(filepath.Join(dir, "gate.lock"), 3*time.Minute)
	if err != nil {
		return State{}, err
	}
	defer releaseGate()
	current, _, err = observe(root)
	if err != nil {
		return State{}, err
	}
	if current.Phase != Freezing || current.CutoverID != id {
		return State{}, errors.New("cutover state changed while draining operations")
	}
	if validateRoot != nil {
		if err := validateRoot(); err != nil {
			aborted := activeState(current.Generation)
			aborted.UpdatedAt = time.Now().UTC()
			if writeErr := writeState(dir, aborted); writeErr != nil {
				return State{}, errors.Join(err, fmt.Errorf("restore active admission: %w", writeErr))
			}
			return aborted, err
		}
	}
	current.Phase = Frozen
	current.UpdatedAt = time.Now().UTC()
	return current, writeState(dir, current)
}

func removeLease(path string) error {
	var err error
	for attempt := 0; attempt < 100; attempt++ {
		err = os.Remove(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if !errors.Is(err, os.ErrPermission) && !platform.IsTransientFileAccess(err) {
			return err
		}
		time.Sleep(2 * time.Millisecond)
	}
	return err
}

func MarkReplaced(root, id string) (State, error) {
	return transition(root, id, Frozen, Replaced)
}

func Activate(root, id string) (State, error) {
	dir, err := Directory(root)
	if err != nil {
		return State{}, err
	}
	releaseGate, err := platform.AcquireFileMutex(filepath.Join(dir, "gate.lock"), 3*time.Minute)
	if err != nil {
		return State{}, err
	}
	defer releaseGate()
	current, _, err := observe(root)
	if err != nil {
		return State{}, err
	}
	if current.Phase != Replaced || current.CutoverID != id {
		if current.Phase == Active && current.LastCutoverID == id {
			return current, nil
		}
		return State{}, fmt.Errorf("activate requires replaced cutover %q, found %s %q", id, current.Phase, current.CutoverID)
	}
	active := activeState(current.Generation + 1)
	active.LastCutoverID = current.CutoverID
	active.LastRelease = current.Release
	active.UpdatedAt = time.Now().UTC()
	return active, writeState(dir, active)
}

func Abort(root, id string) (State, error) {
	dir, err := Directory(root)
	if err != nil {
		return State{}, err
	}
	releaseGate, err := platform.AcquireFileMutex(filepath.Join(dir, "gate.lock"), 3*time.Minute)
	if err != nil {
		return State{}, err
	}
	defer releaseGate()
	current, _, err := observe(root)
	if err != nil {
		return State{}, err
	}
	if (current.Phase != Freezing && current.Phase != Frozen) || current.CutoverID != id {
		return State{}, fmt.Errorf("abort is safe only before replacement for cutover %q", id)
	}
	active := activeState(current.Generation)
	active.UpdatedAt = time.Now().UTC()
	return active, writeState(dir, active)
}

func transition(root, id string, from, to Phase) (State, error) {
	dir, err := Directory(root)
	if err != nil {
		return State{}, err
	}
	releaseGate, err := platform.AcquireFileMutex(filepath.Join(dir, "gate.lock"), 3*time.Minute)
	if err != nil {
		return State{}, err
	}
	defer releaseGate()
	current, _, err := observe(root)
	if err != nil {
		return State{}, err
	}
	if current.Phase == to && current.CutoverID == id {
		return current, nil
	}
	if current.Phase != from || current.CutoverID != id {
		return State{}, fmt.Errorf("transition requires %s cutover %q, found %s %q", from, id, current.Phase, current.CutoverID)
	}
	current.Phase = to
	current.UpdatedAt = time.Now().UTC()
	return current, writeState(dir, current)
}

func leasePaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "leases"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".lease") {
			paths = append(paths, filepath.Join(dir, "leases", entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func writeState(dir string, state State) error {
	if err := validate(state); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := ensureInitialized(dir); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return platform.ReplaceFile(tempPath, filepath.Join(dir, "state.json"))
}

func ensureInitialized(dir string) error {
	path := filepath.Join(dir, "initialized")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := file.Write([]byte("cutover-control-v1\n")); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

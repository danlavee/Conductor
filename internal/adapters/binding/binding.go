// Package binding stores the identity a host session speaks for. A session's
// file exists once its adapter has served it, and holds the agent name once
// the agent joins from inside it.
package binding

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

// ErrInactive means the session no longer acts as the agent offering work.
var ErrInactive = errors.New("adapter session no longer serves this identity")

// Validate rejects a session id that is empty, unsubstituted, or unsafe as a filename.
func Validate(session, placeholder string) error {
	if strings.TrimSpace(session) == "" {
		return errors.New("no session identifier")
	}
	if placeholder != "" && session == placeholder {
		return errors.New("session identifier is the literal " + placeholder + ", so the host did not substitute it")
	}
	for _, r := range session {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return errors.New("session identifier contains an unusable character: " + string(r))
		}
	}
	return nil
}

// Directory is adapters/<name> beside the state root's other durable files.
func Directory(root, adapter string) string {
	return filepath.Join(root, "adapters", adapter)
}

// Identity reads the agent this session is bound to. Absence is an empty name.
func Identity(root, adapter, session string) (string, error) {
	path, err := sessionPath(root, adapter, session)
	if err != nil {
		return "", err
	}
	return readName(path)
}

// Serve records that the adapter's session-start hook ran for session, which
// is what lets a join inside it hand the session to the adapter. An identity
// already bound -- a resumed or compacted session -- stays bound.
func Serve(root, adapter, session string) error {
	path, err := sessionPath(root, adapter, session)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return file.Close()
}

// Bind records that a session the adapter has served acts as agent. A session
// never served, or already forgotten, stays unbound and reports false, so a
// join racing teardown cannot resurrect it.
//
// An agent is bound in one session at a time, and the last join wins: Bind
// takes the agent over from its other sessions (TakeOver) and binds this one
// under the same agent lock, answering the adapter it took the agent from.
func Bind(root, adapter, session, agent string) (bound bool, tookOver string, err error) {
	path, err := sessionPath(root, adapter, session)
	if err != nil {
		return false, "", err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		return false, "", err
	}
	tookOver, err = takeOver(root, agent, path, func() (err error) {
		bound, err = bind(root, adapter, session, agent)
		return err
	})
	return bound, tookOver, err
}

// TakeOver unbinds agent from every session it is bound in, under any
// adapter, for a join that binds no session, and answers the adapter of one it
// unbound ("" for none). An unbound session stays served, so joining again
// there takes the agent back.
func TakeOver(root, agent string) (tookOver string, err error) {
	return takeOver(root, agent, "", func() error { return nil })
}

// takeOver unbinds agent from every session but keep, then runs then, all
// under the agent's lock. The lock order is agent lock, then session mutex,
// one session at a time.
func takeOver(root, agent, keep string, then func() error) (tookOver string, err error) {
	release, err := platform.AcquireFileMutex(filepath.Join(root, "adapters", "agents", agent+".guard"), 3*time.Minute)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, release()) }()
	sessions, err := filepath.Glob(filepath.Join(root, "adapters", "*", "sessions", "*"))
	if err != nil {
		return "", err
	}
	for _, path := range sessions {
		session, adapter := filepath.Base(path), filepath.Base(filepath.Dir(filepath.Dir(path)))
		if Validate(session, "") != nil || path == keep {
			continue // a .guard file, or the session being bound
		}
		unbound, err := unbind(root, adapter, session, agent)
		if err != nil {
			return "", err
		}
		if unbound {
			tookOver = adapter
		}
	}
	return tookOver, then()
}

// unbind empties session's binding if it names agent, leaving it served.
func unbind(root, adapter, session, agent string) (unbound bool, err error) {
	err = withSession(root, adapter, session, SessionWait, func(path string) error {
		current, err := readName(path)
		if err != nil || current != agent {
			return err
		}
		unbound = true
		return platform.WriteFile(path, nil)
	})
	return unbound, err
}

func bind(root, adapter, session, agent string) (bound bool, err error) {
	err = withSession(root, adapter, session, SessionWait, func(path string) error {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		if err := platform.WriteFile(path, []byte(agent)); err != nil {
			return err
		}
		bound = true
		return nil
	})
	return bound, err
}

// SessionWait is how long a session's mutex is waited for by anything not
// running under a host hook's short timeout.
const SessionWait = 3 * time.Minute

// Offer runs action only while session is bound to agent, serialized against
// Bind and Forget, so work handed over under it reaches the identity it was
// meant for. Otherwise it returns ErrInactive and action never runs. It waits
// at most wait for the session, then fails with platform.ErrMutexTimeout.
func Offer(root, adapter, session, agent string, wait time.Duration, action func() error) error {
	return withSession(root, adapter, session, wait, func(path string) error {
		current, err := readName(path)
		if err != nil {
			return err
		}
		if agent == "" || current != agent {
			return ErrInactive
		}
		return action()
	})
}

// Forget removes a session binding. Absence is success.
func Forget(root, adapter, session string) error {
	return withSession(root, adapter, session, SessionWait, platform.Remove)
}

// withSession runs action on session's path under the session's mutex. The
// mutex file outlives the binding: unlinking a held flock file would let a
// later locker lock a different inode.
func withSession(root, adapter, session string, wait time.Duration, action func(path string) error) (err error) {
	path, err := sessionPath(root, adapter, session)
	if err != nil {
		return err
	}
	release, err := platform.AcquireFileMutex(path+".guard", wait)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	return action(path)
}

func sessionPath(root, adapter, session string) (string, error) {
	if err := Validate(session, ""); err != nil {
		return "", err
	}
	return filepath.Join(Directory(root, adapter), "sessions", session), nil
}

func readName(path string) (string, error) {
	data, err := platform.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

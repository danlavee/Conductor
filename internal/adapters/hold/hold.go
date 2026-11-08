// Package hold keeps an agent wakeable for the host session it is bound to,
// the one mechanism every adapter shares. An adapter supplies only its
// session, its target process, and how a delivery enters a turn.
//
// Two roles serve a bound session. The holder owns core's watch guard, which
// is what reads the agent as wakeable. The watcher waits for a delivery and
// hands it to the host. Its waker record exists from the moment it registers
// until it hands a delivery over, so a record naming a dead process means a
// watcher ended without handing over. A missing record means a delivery
// started a turn, whose end runs the next watcher.
package hold

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/platform"
	"github.com/danlavee/Conductor/internal/state"
)

// Session is one host session an adapter serves, and the process that hosts it.
type Session struct {
	Binding string // binding directory, also the CLI host token: claude, grok, agy, codex
	ID      string
	Target  platform.Process
}

// AncestorTarget records the first process from pid up its parents that skip
// passes over, for a host whose hooks do not run as children of the session.
func AncestorTarget(pid int, skip func(platform.ProcessInstance) bool) (platform.Process, error) {
	for {
		process, err := platform.InspectProcess(pid)
		if err != nil {
			return platform.Process{}, err
		}
		if !skip(process) {
			return process.Process, nil
		}
		pid = process.ParentPID
	}
}

// Hold keeps client's agent wakeable for s until the target dies, s stops naming
// the agent, a registered watcher dies without handing over, serve returns, or
// ctx ends. LOCKED, NOT_FOUND and ErrInactive are nil. serve may be nil.
//
// Only a detached holder (serve nil) reads the waker record: an in-process
// watcher dies with its holder. The holder never removes that record, so it
// cannot delete a live watcher's registration. A holder that finds the guard
// LOCKED may be meeting an old holder that has yet to see its session lose
// the agent, so it waits: a detached one while the record names a live
// watcher, which may be this one's, and an in-process one while s still keeps
// the agent and the owner holds for another session -- one holding for s
// already does this one's job.
func Hold(ctx context.Context, client *state.Client, s Session, serve func(context.Context) error) (err error) {
	_, waker := records(client)
	name := s.Binding + "/" + s.ID
	var release func() error
	for {
		err = binding.Offer(client.Home, s.Binding, s.ID, client.Agent, binding.SessionWait, func() (err error) {
			release, err = client.AcquireWatchOwnership(state.HolderAdapter, name)
			return err
		})
		if protocolCode(err) != "LOCKED" {
			break
		}
		if serve == nil {
			if live, err := watcherLive(waker, false); !live || err != nil {
				return err
			}
		} else if ok, err := keeps(client, s); !ok || err != nil {
			return err
		} else if owner, err := client.WatchOwnerSession(); owner == name || err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(client.PollInterval):
		}
	}
	if declined(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	served := make(chan error, 1)
	if serve == nil {
		served <- nil
	} else {
		go func() { served <- serve(ctx); cancel() }()
	}
	err = poll(ctx, client.PollInterval, func() (bool, error) {
		if ok, err := keeps(client, s); !ok || err != nil || serve != nil {
			return ok, err
		}
		return watcherLive(waker, true)
	})
	cancel()
	return errors.Join(err, <-served)
}

// Watch waits for one delivery as the agent's registered watcher and passes it
// to deliver, which returns the deliveries it handed to the host. Only those
// are acknowledged, and only after deliver returns nil; delivered reports that.
// A busy watch lock, a dead target, a rebind, a Forget, the agent leaving, and
// ctx ending all return (false, nil).
func Watch(ctx context.Context, client *state.Client, s Session,
	deliver func(state.Batch) ([]state.ReadResult, error)) (delivered bool, err error) {
	return watch(ctx, client, s, deliver, func() error { return nil })
}

// Arm is Watch for a hook process: after registering, it spawns the detached
// holder, which takes over if the current one releases while this watcher
// lives, and otherwise exits.
func Arm(ctx context.Context, client *state.Client, s Session,
	deliver func(state.Batch) ([]state.ReadResult, error)) (delivered bool, err error) {
	return watch(ctx, client, s, deliver, func() error { return spawnHolder(client.Home, s) })
}

func watch(ctx context.Context, client *state.Client, s Session,
	deliver func(state.Batch) ([]state.ReadResult, error), registered func() error) (delivered bool, err error) {
	lock, waker := records(client)
	// A registered watcher is live: arming on every tool result must cost no
	// wait then. The timeout rides out a predecessor still exiting, which
	// removed its record when it handed over. A record that cannot be read
	// names no one, and is replaced below.
	if live, _ := watcherLive(waker, false); live {
		return false, nil
	}
	release, err := platform.AcquireFileMutex(lock, time.Second)
	if errors.Is(err, platform.ErrMutexTimeout) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, release()) }()
	self, err := platform.ProcessOf(os.Getpid())
	if err != nil {
		return false, err
	}
	if err := writeWaker(waker, self); err != nil {
		return false, err
	}
	if err := registered(); err != nil {
		return false, err
	}

	// The wait only says there is work. What is delivered is read again under
	// the session's lock, since an inject may have shown it meanwhile; finding
	// nothing, the watcher waits again, still registered.
	for {
		if ready, err := awaitWork(ctx, client, s); !ready {
			return false, err
		}
		err = binding.Offer(client.Home, s.Binding, s.ID, client.Agent, binding.SessionWait, func() error {
			if !s.Target.Alive() {
				return binding.ErrInactive
			}
			batch, err := client.Unread()
			if err != nil || len(batch.Deliveries) == 0 {
				return err
			}
			handed, err := deliver(batch)
			if err != nil {
				return err
			}
			delivered = true
			if err := platform.Remove(waker); err != nil {
				return err
			}
			return client.AcknowledgeRead(handed...)
		})
		if errors.Is(err, binding.ErrInactive) {
			return false, nil
		}
		if delivered || err != nil {
			return delivered, err
		}
	}
}

// awaitWork blocks until the agent has unread work while s can still wake it.
// It reports false, with a nil error, when s can no longer wake it or ctx ends.
func awaitWork(ctx context.Context, client *state.Client, s Session) (ready bool, err error) {
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	polled := make(chan error, 1)
	go func() {
		polled <- poll(watchCtx, client.PollInterval, func() (bool, error) { return keeps(client, s) })
		cancel()
	}()
	_, err = client.WatchContext(watchCtx)
	cancel()
	if pollErr := <-polled; pollErr != nil {
		return false, pollErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || declined(err) {
		return false, nil
	}
	return err == nil, err
}

// InjectWait bounds Inject's wait for its session, well inside a host hook's
// timeout: a hook the host kills holds up the turn it runs in.
const InjectWait = 2 * time.Second

// Inject hands s what is unread now, from a hook that runs inside a turn, and
// acknowledges exactly the deliveries deliver returns; delivered reports that
// deliver did. It never touches the watch lock or the waker record, so the
// holder keeps holding through the turn. A session it cannot take within
// InjectWait, or no longer bound to the agent, gets nothing and everything
// stays unread.
//
// Most hooks find nothing, and reading every topic's history to learn that
// grows with the bus, so the heads of the last empty read are kept and an
// inject whose heads match them reads nothing.
func Inject(client *state.Client, s Session, deliver func(state.Batch) ([]state.ReadResult, error)) (delivered bool, err error) {
	heads, err := client.Heads()
	if err != nil {
		return false, err
	}
	path := quietHeads(client)
	if kept, err := platform.ReadFile(path); err == nil {
		var quiet map[string]int64
		if json.Unmarshal(kept, &quiet) == nil && maps.Equal(quiet, heads) {
			return false, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	err = binding.Offer(client.Home, s.Binding, s.ID, client.Agent, InjectWait, func() error {
		batch, err := client.Unread()
		if err != nil {
			return err
		}
		if len(batch.Deliveries) == 0 {
			// Taken before the read, so anything since changes them.
			data, err := json.Marshal(heads)
			if err != nil {
				return err
			}
			return platform.WriteFile(path, data)
		}
		handed, err := deliver(batch)
		if err != nil {
			return err
		}
		delivered = true
		return client.AcknowledgeRead(handed...)
	})
	if errors.Is(err, platform.ErrMutexTimeout) || declined(err) {
		return false, nil
	}
	return delivered, err
}

// spawnHolder starts `adapter <binding> hold` detached from the hook, with no
// inherited std handles and its working directory off the user's project.
func spawnHolder(root string, s Session) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "adapter", s.Binding, "hold", "--session="+s.ID, "--target="+s.Target.String())
	command.Dir = root
	command.Env = append(os.Environ(), "CONDUCTOR_HOME="+root)
	detach(command)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

// keeps reports whether s can still wake its agent: the target runs and the
// session is still bound to the agent.
func keeps(client *state.Client, s Session) (bool, error) {
	if !s.Target.Alive() {
		return false, nil
	}
	agent, err := binding.Identity(client.Home, s.Binding, s.ID)
	return err == nil && agent == client.Agent, err
}

// watcherLive reports whether the waker record names a live process. absent
// is the answer when there is no record: a watcher handed a delivery over.
func watcherLive(waker string, absent bool) (bool, error) {
	data, err := platform.ReadFile(waker)
	if errors.Is(err, os.ErrNotExist) {
		return absent, nil
	}
	if err != nil {
		return false, err
	}
	var watcher platform.Process
	if err := json.Unmarshal(data, &watcher); err != nil {
		return false, err
	}
	return watcher.Alive(), nil
}

// poll runs check every interval until it reports false or fails, or ctx ends.
func poll(ctx context.Context, interval time.Duration, check func() (bool, error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		if ok, err := check(); !ok || err != nil {
			return err
		}
	}
}

// declined is an outcome that leaves nothing to hold: another holder owns the
// agent, the agent is not on the roster, or the session no longer names it.
func declined(err error) bool {
	code := protocolCode(err)
	return code == "LOCKED" || code == "NOT_FOUND" || errors.Is(err, binding.ErrInactive)
}

func protocolCode(err error) string {
	var protocol *state.ProtocolError
	if errors.As(err, &protocol) {
		return protocol.Code
	}
	return ""
}

// quietHeads locates the heads of the agent's last read that found nothing.
func quietHeads(client *state.Client) string {
	return filepath.Join(client.Home, "adapters", "watch", client.Agent+".heads.json")
}

// records locates the agent's watch lock and waker record.
func records(client *state.Client) (lock, waker string) {
	base := filepath.Join(client.Home, "adapters", "watch", client.Agent)
	return base + ".guard", base + ".json"
}

func writeWaker(path string, watcher platform.Process) error {
	data, err := json.Marshal(watcher)
	if err != nil {
		return err
	}
	return platform.WriteFile(path, data)
}

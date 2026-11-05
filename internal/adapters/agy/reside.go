package agy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/platform"
	"github.com/danlavee/Conductor/internal/state"
)

const (
	// HostPIDEnvironment is an optional PID of the Antigravity host process;
	// when set, Reside automatically releases ownership if the host process exits.
	HostPIDEnvironment = "CONDUCTOR_AGY_HOST_PID"
	// HostStartEnvironment is the optional process start token for HostPIDEnvironment.
	HostStartEnvironment = "CONDUCTOR_AGY_HOST_START"
)

// Reside holds the adapter watch ownership guard for client.Agent across turns,
// resolves incoming deliveries into receipts, and writes presentation files for
// the Stop hook to surface at turn boundaries. It never acknowledges deliveries.
func Reside(ctx context.Context, client *state.Client, session string) error {
	if err := binding.Validate(session, ""); err != nil {
		return err
	}
	status, err := client.WatchStatus()
	if err != nil {
		return err
	}
	if !status.Registered {
		return nil
	}
	if releaseRequested(client.Home, client.Agent, session) {
		_ = clearRelease(client.Home, client.Agent)
		return nil
	}
	release, err := client.AcquireWatchOwnership(state.HolderAdapter)
	if err != nil {
		var pErr *state.ProtocolError
		if errors.As(err, &pErr) && (pErr.Code == "LOCKED" || pErr.Code == "NOT_FOUND") {
			return nil
		}
		return err
	}
	defer release()

	start, _ := platform.ProcessStartToken(os.Getpid())
	hostPID, hostStart := trackedHostProcess()
	if err := writeResident(client.Home, residentRecord{
		Agent:        client.Agent,
		Session:      session,
		PID:          os.Getpid(),
		ProcessStart: start,
		HostPID:      hostPID,
		HostStart:    hostStart,
		StartedAt:    time.Now().UTC(),
	}); err != nil {
		return err
	}
	defer func() {
		if ResidentOwnedBy(client.Home, client.Agent, session, os.Getpid()) {
			_ = removeResident(client.Home, client.Agent)
		}
	}()
	if releaseRequested(client.Home, client.Agent, session) {
		_ = clearRelease(client.Home, client.Agent)
		return nil
	}

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				if releaseRequested(client.Home, client.Agent, session) ||
					!ResidentOwnedBy(client.Home, client.Agent, session, os.Getpid()) ||
					!hostProcessAlive(hostPID, hostStart) {
					cancel()
					return
				}
			}
		}
	}()

	for {
		if watchCtx.Err() != nil {
			_ = clearRelease(client.Home, client.Agent)
			return nil
		}
		shown, err := loadPresentation(client.Home, client.Agent)
		if err != nil {
			return err
		}
		if shown != nil {
			select {
			case <-watchCtx.Done():
				_ = clearRelease(client.Home, client.Agent)
				return nil
			case <-time.After(40 * time.Millisecond):
				continue
			}
		}
		batch, err := client.WatchContext(watchCtx)
		if err != nil {
			if watchCtx.Err() != nil || errors.Is(err, context.Canceled) {
				_ = clearRelease(client.Home, client.Agent)
				return nil
			}
			var pErr *state.ProtocolError
			if errors.As(err, &pErr) && pErr.Code == "NOT_FOUND" {
				return nil
			}
			if isBusy(err) {
				if waitContext(watchCtx, 40*time.Millisecond) {
					continue
				}
				return nil
			}
			return err
		}
		if err := stageWatchBatch(client, session, start, batch); err != nil {
			var pErr *state.ProtocolError
			if errors.As(err, &pErr) && pErr.Code == "NOT_FOUND" {
				return nil
			}
			if isBusy(err) {
				if waitContext(watchCtx, 40*time.Millisecond) {
					continue
				}
				return nil
			}
			return err
		}
	}
}

func stageWatchBatch(client *state.Client, session, ownerStart string, batch state.Batch) error {
	if releaseRequested(client.Home, client.Agent, session) || !ResidentOwnedBy(client.Home, client.Agent, session, os.Getpid()) {
		return nil
	}
	if len(batch.Deliveries) == 0 {
		return nil
	}
	receipt, err := state.IssueReceipt(client.Agent, batch)
	if err != nil {
		return err
	}
	token, err := receiptToken(receipt)
	if err != nil {
		return err
	}
	return writePresentation(client.Home, client.Agent, presentation{
		Kind:       "delivery",
		Phase:      "delivered",
		Session:    session,
		Token:      token,
		Receipt:    receipt,
		OwnerPID:   os.Getpid(),
		OwnerStart: ownerStart,
	})
}

func isBusy(err error) bool {
	var pErr *state.ProtocolError
	if errors.As(err, &pErr) && pErr.Code == "LOCKED" {
		return true
	}
	return platform.IsTransientFileAccess(err)
}

func waitContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// EnsureResident spawns a detached reside process for the session's bound
// agent if one is not already alive, and waits until it acquires the watch guard.
func EnsureResident(root, session, project string) error {
	agent, err := ResolveAgent(root, session, project)
	if err != nil || agent == "" {
		return err
	}
	client, err := state.Open(root, agent)
	if err != nil {
		return err
	}
	status, err := client.WatchStatus()
	if err != nil || !status.Registered {
		return err
	}
	if ResidentAlive(root, agent, session) {
		return nil
	}
	if rec, err := loadResident(root, agent); err == nil && rec != nil && rec.Session != session && processAlive(rec.PID, rec.ProcessStart) {
		return errors.New("another session already holds the delivery stream for " + agent)
	}
	if status.Wakeable {
		return errors.New("another watcher already holds the delivery stream for " + agent)
	}
	if err := clearRelease(root, agent); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()

	command := exec.Command(executable, "adapter", AdapterName, "reside", "--session="+session)
	command.Env = append(os.Environ(), "CONDUCTOR_HOME="+root)
	if strings.TrimSpace(project) != "" {
		command.Dir = project
	}
	command.Stdin = devNull
	command.Stdout = devNull
	command.Stderr = devNull
	detach(command)
	if err := command.Start(); err != nil {
		return err
	}
	childPID := command.Process.Pid
	_ = command.Process.Release()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ResidentAlive(root, agent, session) {
			return nil
		}
		if !platform.ProcessAlive(childPID) {
			if ResidentAlive(root, agent, session) {
				return nil
			}
			return errors.New("agy resident exited before acquiring the guard")
		}
		time.Sleep(15 * time.Millisecond)
	}
	_ = requestRelease(root, agent, session)
	if proc, findErr := os.FindProcess(childPID); findErr == nil {
		_ = proc.Kill()
	}
	return errors.New("agy resident did not acquire the guard")
}

// ResidentAlive reports whether a live reside process owns agent for session.
func ResidentAlive(root, agent, session string) bool {
	rec, err := loadResident(root, agent)
	if err != nil || rec == nil {
		return false
	}
	if session != "" && rec.Session != session {
		return false
	}
	return processAlive(rec.PID, rec.ProcessStart)
}

// ResidentOwnedBy reports whether pid is still the recorded resident for agent and session.
func ResidentOwnedBy(root, agent, session string, pid int) bool {
	rec, err := loadResident(root, agent)
	if err != nil || rec == nil {
		return false
	}
	return rec.Session == session && rec.PID == pid
}

// StopResident stops the resident process for agent owned by session.
func StopResident(root, agent, session string) (bool, error) {
	return stopResident(root, agent, session)
}

func stopResident(root, agent, session string) (bool, error) {
	rec, err := loadResident(root, agent)
	if err != nil || rec == nil {
		return false, err
	}
	if session != "" && rec.Session != session {
		return false, nil
	}
	if !processAlive(rec.PID, rec.ProcessStart) {
		_ = removeResident(root, agent)
		return false, nil
	}
	if err := requestRelease(root, agent, rec.Session); err != nil {
		return false, err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(rec.PID, rec.ProcessStart) {
			_ = removeResident(root, agent)
			_ = clearRelease(root, agent)
			return true, nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	if proc, findErr := os.FindProcess(rec.PID); findErr == nil {
		_ = proc.Kill()
	}
	_ = removeResident(root, agent)
	_ = clearRelease(root, agent)
	return true, nil
}

func trackedHostProcess() (int, string) {
	rawPID := strings.TrimSpace(os.Getenv(HostPIDEnvironment))
	if rawPID == "" {
		return 0, ""
	}
	pid, err := strconv.Atoi(rawPID)
	if err != nil || pid <= 0 {
		return 0, ""
	}
	start := strings.TrimSpace(os.Getenv(HostStartEnvironment))
	if start == "" {
		start, _ = platform.ProcessStartToken(pid)
	}
	return pid, start
}

func hostProcessAlive(pid int, start string) bool {
	if pid <= 0 {
		return true
	}
	return processAlive(pid, start)
}

func processAlive(pid int, start string) bool {
	if pid <= 0 || !platform.ProcessAlive(pid) {
		return false
	}
	if start == "" {
		return true
	}
	token, ok := platform.ProcessStartToken(pid)
	return ok && token == start
}

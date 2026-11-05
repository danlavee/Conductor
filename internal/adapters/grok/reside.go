package grok

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
	"github.com/danlavee/Conductor/internal/state"
)

// ErrResidentDown means the resident is gone and has not been released.
var ErrResidentDown = errors.New("grok resident is not holding the stream")

// Reside holds the agent's stream until release. It never acknowledges.
func Reside(ctx context.Context, client *state.Client, session string) error {
	root := client.Home
	if set, err := flagSet(root, "cancel", session); err != nil {
		return err
	} else if set {
		return nil
	}
	release, err := client.AcquireWatchOwnership(state.HolderAdapter)
	if err != nil {
		var protocolErr *state.ProtocolError
		if errors.As(err, &protocolErr) && (protocolErr.Code == "LOCKED" || protocolErr.Code == "NOT_FOUND") {
			return nil
		}
		return err
	}
	defer release()
	start, _ := platform.ProcessStartToken(os.Getpid())
	if err := saveResidency(root, client.Agent, residency{Session: session, PID: os.Getpid(), Start: start}); err != nil {
		return err
	}
	if set, err := flagSet(root, "cancel", session); err != nil || set {
		return err
	}
	if err := setFlag(root, "ready", session); err != nil {
		return err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				live, err := loadResidency(root, client.Agent)
				canceled, flagErr := flagSet(root, "cancel", session)
				if err != nil || flagErr != nil || canceled || live == nil || live.Released || live.Session != session {
					cancel()
					return
				}
			}
		}
	}()
	for {
		if shown, err := loadPresentation(root, client.Agent); err != nil {
			return err
		} else if shown != nil {
			if !waitPoll(streamCtx) {
				return nil
			}
			continue
		}
		batch, err := client.WatchContext(streamCtx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			var protocolErr *state.ProtocolError
			if errors.As(err, &protocolErr) && protocolErr.Code == "NOT_FOUND" {
				return nil
			}
			if busy(err) && waitPoll(streamCtx) {
				continue
			}
			if busy(err) {
				return nil
			}
			return err
		}
		live, err := loadResidency(root, client.Agent)
		if err != nil || live == nil || live.Released || live.Session != session {
			return err
		}
		receipt, err := state.IssueReceipt(client.Agent, batch)
		if err != nil {
			if busy(err) && waitPoll(streamCtx) {
				continue
			}
			return err
		}
		token, err := receiptToken(receipt)
		if err != nil {
			return err
		}
		shown := Presentation{Session: session, OwnerPID: os.Getpid(), OwnerStart: start, Token: token, Receipt: receipt}
		if err := savePresentation(root, client.Agent, shown); err != nil {
			return err
		}
	}
}

func busy(err error) bool {
	var protocolErr *state.ProtocolError
	if errors.As(err, &protocolErr) && protocolErr.Code == "LOCKED" {
		return true
	}
	return platform.IsTransientFileAccess(err)
}

func waitPoll(ctx context.Context) bool {
	timer := time.NewTimer(PollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

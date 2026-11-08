package main

import (
	"errors"
	"fmt"
)

// wakeSignal is a successful wake, carried as an error only so the exit status
// survives out of run().
type wakeSignal struct{ Code int }

func (w *wakeSignal) Error() string { return fmt.Sprintf("wake exit %d", w.Code) }

// runAdapterCommand routes a host's hook to that host. Each host owns its own
// usage, so the verbs only hooks run never reach the surface agents read.
func runAdapterCommand(args []string) error {
	return adapterHostsUsageError()
}

func adapterHostsUsageError() error {
	return errors.New("usage: conductor adapter <host> <command>")
}

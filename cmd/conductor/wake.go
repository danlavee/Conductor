package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/adapters/hold"
	"github.com/danlavee/Conductor/internal/install"
	"github.com/danlavee/Conductor/internal/platform"
	conductor "github.com/danlavee/Conductor/internal/state"
)

// deliverFunc writes a batch the way a host reads it and returns the
// deliveries it showed.
type deliverFunc func(conductor.Batch) ([]conductor.ReadResult, error)

// adapterHost is everything one host's adapter adds to the shared hooks: its
// session-start hook records each session, the host exports the session id to
// every command the model runs, and runAdapterCommand runs each hook verb.
type adapterHost struct {
	binding     string // binding directory and CLI host token
	environment string
	payload     install.Payload
	verbs       []string // hook verbs: identity, arm, and optionally inject and release
	input       string   // how a hook names its session, for the usage
	// event reads which session a hook runs in and whether verb acts on this
	// event at all: a subagent's, or a turn a delivery may not continue, does not.
	event    func(verb string, args []string, stdin io.Reader) (session string, act bool, err error)
	target   func() (platform.Process, error) // the session process the holder serves
	wakeCode int                              // the exit status of a hook that delivered
	wake     deliverFunc
	inject   deliverFunc              // for the inject verb
	announce func(agent string) error // identity's output, if any
	idle     func() error             // what a hook that delivers nothing writes, if anything
	// serve, if set, makes arm hold the session in process instead: Hold owns
	// the agent's guard until serve returns.
	serve func(ctx context.Context, client *conductor.Client, s hold.Session) error
}

var adapterHosts []adapterHost

func (h adapterHost) usage() error {
	return fmt.Errorf("usage: conductor adapter %s <%s> %s | install <absolute-adapter-directory> [--replace]",
		h.binding, strings.Join(h.verbs, "|"), h.input)
}

func adapterHostsUsageError() error {
	names := make([]string, len(adapterHosts))
	for i, host := range adapterHosts {
		names[i] = host.binding
	}
	return fmt.Errorf("usage: conductor adapter <%s> <command>", strings.Join(names, "|"))
}

// bindWake answers join's wake verdict. A join from inside a session an
// adapter serves binds that session to the agent, so the adapter holds it from
// then on and joining is the agent's whole part; anywhere else the agent holds
// its own stream with watch. Either way the last join wins: tookOver names the
// host of the session the agent was bound in before, which this join unbound,
// so its holder releases and cannot lock the new watcher out.
func bindWake(root, agent string) (wake, tookOver string, err error) {
	for _, host := range adapterHosts {
		session := os.Getenv(host.environment)
		if session == "" {
			continue
		}
		if bound, tookOver, err := binding.Bind(root, host.binding, session, agent); bound || err != nil {
			return "adapter", tookOver, err
		}
	}
	tookOver, err = binding.TakeOver(root, agent)
	return "watch", tookOver, err
}

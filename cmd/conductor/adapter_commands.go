package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/adapters/hold"
	"github.com/danlavee/Conductor/internal/install"
	"github.com/danlavee/Conductor/internal/platform"
	conductor "github.com/danlavee/Conductor/internal/state"
)

// wakeSignal is a successful wake, carried as an error only so the exit status
// survives out of run().
type wakeSignal struct{ Code int }

func (w *wakeSignal) Error() string { return fmt.Sprintf("wake exit %d", w.Code) }

// errUsage is a hook invoked with arguments of the wrong shape; the dispatcher
// answers it with the host's usage.
var errUsage = errors.New("usage")

// sessionFlag carries the host's substituted session identifier. A flag rather
// than a positional, so a lost substitution is a usage error, not a value.
const sessionFlag = "--session="

// targetFlag names the process a detached holder keeps the session wakeable for.
const targetFlag = "--target="

// runAdapterCommand runs a host's hook. Each host owns its own usage, so the
// verbs only hooks run never reach the surface agents read.
func runAdapterCommand(args []string) error {
	i := slices.IndexFunc(adapterHosts, func(h adapterHost) bool { return len(args) > 0 && h.binding == args[0] })
	if i < 0 {
		return adapterHostsUsageError()
	}
	host := adapterHosts[i]
	if len(args) < 2 {
		return host.usage()
	}
	verb, rest := args[1], args[2:]
	switch {
	case verb == "install":
		return runAdapterInstall(host.binding, host.payload, rest)
	case verb == "hold":
		return runHold(host.binding, rest)
	case !slices.Contains(host.verbs, verb):
		return host.usage()
	}
	session, act, err := host.event(verb, rest, os.Stdin)
	if errors.Is(err, errUsage) {
		return host.usage()
	}
	if err != nil {
		return err
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	if act {
		switch verb {
		case "identity":
			err = binding.Serve(root, host.binding, session)
			if err == nil && host.announce != nil {
				agent, err := binding.Identity(root, host.binding, session)
				if err != nil {
					return err
				}
				return host.announce(agent)
			}
		case "release":
			err = binding.Forget(root, host.binding, session)
		case "inject":
			return runAdapterInject(root, host.binding, session, host.inject)
		case "arm":
			err = runAdapterArm(root, host, session)
		}
	}
	if err != nil || host.idle == nil {
		return err
	}
	return host.idle()
}

// hookSession takes the session identifier from the substituted argument. A
// missing one fails loudly: every session would otherwise scope teardown to
// the same empty identifier and tear down each other's holds.
func hookSession(args []string, placeholder string) (string, error) {
	if len(args) != 1 || !strings.HasPrefix(args[0], sessionFlag) {
		return "", errUsage
	}
	session := strings.TrimPrefix(args[0], sessionFlag)
	if err := binding.Validate(session, placeholder); err != nil {
		return "", fmt.Errorf("%s: %w", sessionFlag, err)
	}
	return session, nil
}

// runHold is the detached holder a host's watcher spawns. It holds the session's
// agent until hold.Hold releases, and is a no-op for an unbound session.
func runHold(host string, args []string) error {
	usage := fmt.Errorf("usage: conductor adapter %s hold %s<id> %s<pid:start>", host, sessionFlag, targetFlag)
	if len(args) != 2 || !strings.HasPrefix(args[1], targetFlag) {
		return usage
	}
	session, err := hookSession(args[:1], "")
	if errors.Is(err, errUsage) {
		return usage
	}
	if err != nil {
		return err
	}
	target, err := platform.ParseProcess(strings.TrimPrefix(args[1], targetFlag))
	if err != nil {
		return err
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	agent, err := binding.Identity(root, host, session)
	if err != nil || agent == "" {
		return err
	}
	client, err := conductor.Open(root, agent)
	if err != nil {
		return err
	}
	return hold.Hold(context.Background(), client, hold.Session{Binding: host, ID: session, Target: target}, nil)
}

// runAdapterArm is a host session's watcher, and spawns its holder; target
// finds the session process. It exits with wakeCode exactly when a
// delivery reached stdout, and cleanly otherwise: an unbound session or a
// declined arm has done its job. A host that serves the session in process
// holds it there instead, until serve returns.
//
// The delivery decides the wake before the error does. Once the payload is on
// stdout, a later failure must not turn it into a silent exit 1 -- waking
// anyway costs a duplicate, not waking costs the turn.
func runAdapterArm(root string, host adapterHost, session string) error {
	agent, err := binding.Identity(root, host.binding, session)
	if err != nil || agent == "" {
		return err
	}
	client, err := conductor.Open(root, agent)
	if err != nil {
		return err
	}
	s := hold.Session{Binding: host.binding, ID: session}
	if s.Target, err = host.target(); err != nil {
		return err
	}
	if host.serve != nil {
		return hold.Hold(context.Background(), client, s, func(ctx context.Context) error { return host.serve(ctx, client, s) })
	}
	delivered, err := hold.Arm(context.Background(), client, s, host.wake)
	if !delivered {
		return err
	}
	if err != nil {
		_ = conductor.WriteJSON(os.Stderr, map[string]string{"message": err.Error()})
	}
	return &wakeSignal{Code: host.wakeCode}
}

// runAdapterInject hands a bound session its unread work from a hook inside a
// turn. An unbound session, and
// one whose work another deliverer holds, get nothing.
//
// Once the output is written the exit is clean whatever follows: a host drops
// the context of a hook that fails, so a failed acknowledgement would lose
// what was shown, while exiting cleanly only shows it again next time.
func runAdapterInject(root, host, session string, deliver deliverFunc) error {
	agent, err := binding.Identity(root, host, session)
	if err != nil || agent == "" {
		return err
	}
	client, err := conductor.Open(root, agent)
	if err != nil {
		return err
	}
	delivered, err := hold.Inject(client, hold.Session{Binding: host, ID: session}, deliver)
	if delivered {
		return nil
	}
	return err
}

// hookParent is the session process of a host that spawns hooks in exec form,
// with no shell between: the hook's parent.
func hookParent() (platform.Process, error) { return platform.ProcessOf(os.Getppid()) }

// stopDelivery is the Stop output Grok and AGY read: a decision the host's
// loop continues on, with reason as the session's next prompt.
type stopDelivery struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// runAdapterInstall places host's tree and the executable its hooks name, and
// stops there. Enabling stays a host command the user runs, because a host's
// plugin registry is private state with no documented contract.
func runAdapterInstall(host string, payload install.Payload, args []string) error {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("usage: conductor adapter %s install <absolute-adapter-directory> [--replace]", host)
	}
	replace, err := replaceRequested(args[1:])
	if err != nil {
		return err
	}
	return runInstall(payload, args[0], replace)
}

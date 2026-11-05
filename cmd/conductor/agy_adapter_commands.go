package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/agy"
	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/adapters/claude"
	"github.com/danlavee/Conductor/internal/install"
	conductor "github.com/danlavee/Conductor/internal/state"
)

const agyIdentityAdvice = "If this is not the identity you were told to act as, run: conductor adapter agy bind <your-agent-name>. " +
	"It binds this session alone, outranks the project's .conductor-agent, leaves that file and other sessions untouched, " +
	"and starts holding your delivery stream across turns. Run it once, and never for an agent that is not you."

type agyIdentityReport struct {
	Adapter      string `json:"adapter"`
	Agent        string `json:"agent"`
	Source       string `json:"source"`
	Session      string `json:"session,omitempty"`
	Project      string `json:"project,omitempty"`
	ProjectAgent string `json:"project_agent,omitempty"`
	Root         string `json:"root,omitempty"`
	Advice       string `json:"advice"`
}

func runAGYAdapterCommand(args []string) error {
	if len(args) < 1 {
		return agyAdapterUsageError()
	}
	switch args[0] {
	case "install":
		return runAGYInstall(args[1:])
	case "bind":
		return runAGYBind(args[1:])
	case "takeover":
		return runAGYTakeover(args[1:])
	case "identity":
		return runAGYIdentity(args[1:])
	case "reside":
		return runAGYReside(args[1:])
	case "stop":
		return runAGYStop(args[1:])
	case "release":
		return runAGYRelease(args[1:])
	default:
		return agyAdapterUsageError()
	}
}

func runAGYIdentity(args []string) error {
	hook, explicitSession, err := parseAGYHookFlags(args)
	if err != nil {
		return err
	}
	var in agy.HookInput
	var session string
	if hook {
		in, err = agy.ParseHookInput(os.Stdin)
		if err != nil {
			return err
		}
		session, err = in.HookSession(explicitSession)
	} else {
		session, err = in.Session(explicitSession)
	}
	if err != nil {
		return err
	}
	project, err := in.Project()
	if err != nil {
		return err
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	resolution, err := agy.Resolve(root, session, project)
	if err != nil {
		return err
	}
	if hook {
		armErr := agy.EnsureResident(root, session, project)
		message := agy.Announcement(root, session, project, resolution)
		if armErr != nil {
			message += " (Note: resident did not arm yet: " + armErr.Error() + ")"
		}
		out := agy.SessionStartOutput{
			InjectSteps: []agy.InjectedStep{
				{EphemeralMessage: message},
			},
		}
		return conductor.WriteJSON(os.Stdout, out)
	}
	agent, source := resolution.Binding()
	return conductor.WriteJSON(os.Stdout, agyIdentityReport{
		Adapter:      agy.AdapterName,
		Agent:        agent,
		Source:       source,
		Session:      session,
		Project:      project,
		ProjectAgent: resolution.Project,
		Root:         root,
		Advice:       agyIdentityAdvice,
	})
}

func runAGYReside(args []string) error {
	hook, explicitSession, err := parseAGYHookFlags(args)
	if err != nil {
		return err
	}
	if hook || strings.TrimSpace(explicitSession) == "" {
		return agyAdapterUsageError()
	}
	var in agy.HookInput
	session, err := in.HookSession(explicitSession)
	if err != nil {
		return err
	}
	project, err := in.Project()
	if err != nil {
		return err
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	agent, err := agy.ResolveAgent(root, session, project)
	if err != nil || agent == "" {
		return err
	}
	client, err := conductor.Open(root, agent)
	if err != nil {
		return err
	}
	return agy.Reside(context.Background(), client, session)
}

func runAGYStop(args []string) error {
	hook, explicitSession, err := parseAGYHookFlags(args)
	if err != nil {
		return err
	}
	in, err := agy.ParseHookInput(os.Stdin)
	if err != nil {
		return err
	}
	if hook {
		if _, err := in.HookSession(explicitSession); err != nil {
			return err
		}
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	out, err := agy.Stop(context.Background(), root, in, explicitSession)
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, out)
}

func runAGYRelease(args []string) error {
	hook, explicitSession, err := parseAGYHookFlags(args)
	if err != nil {
		return err
	}
	if hook {
		return agyAdapterUsageError()
	}
	var in agy.HookInput
	session, err := in.Session(explicitSession)
	if err != nil {
		return err
	}
	project, err := in.Project()
	if err != nil {
		return err
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	agent, released, err := agy.Release(root, session, project)
	if err != nil {
		return err
	}
	outcome := claude.NotOwned
	if released {
		outcome = claude.Released
	} else if agent == "" {
		outcome = claude.Unbound
	}
	return conductor.WriteJSON(os.Stderr, adapterReport{
		Command: "release",
		Outcome: string(outcome),
		Agent:   agent,
	})
}

func runAGYBind(args []string) error {
	if len(args) < 1 || len(args) > 2 || strings.TrimSpace(args[0]) == "" {
		return agyBindUsageError()
	}
	agent := strings.TrimSpace(args[0])
	session := os.Getenv(agy.SessionEnvironment)
	if len(args) == 2 {
		if !strings.HasPrefix(args[1], sessionFlag) {
			return agyBindUsageError()
		}
		session = strings.TrimPrefix(args[1], sessionFlag)
	}
	if err := binding.Validate(session, ""); err != nil {
		return fmt.Errorf("%s: %w", agy.SessionEnvironment, err)
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	client, err := conductor.Open(root, agent)
	if err != nil {
		return err
	}
	status, err := client.WatchStatus()
	if err != nil {
		return err
	}
	if !status.Registered {
		return errors.New("cannot bind to " + agent + ": it is not on the roster, so join first")
	}
	previous, _ := binding.Identity(root, agy.AdapterName, session)
	var previousProject string
	if wd, wdErr := os.Getwd(); wdErr == nil {
		if proj, projErr := agy.ProjectDirectory(wd); projErr == nil {
			previousProject, _ = agy.ResolveProject(proj)
		}
	}
	if err := binding.Bind(root, agy.AdapterName, session, agent); err != nil {
		return err
	}
	if previous != "" && previous != agent {
		_, _ = agy.StopResident(root, previous, session)
	} else if previous == "" && previousProject != "" && previousProject != agent {
		_, _ = agy.StopResident(root, previousProject, session)
	}
	if err := agy.EnsureResident(root, session, ""); err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, map[string]string{
		"adapter":  agy.AdapterName,
		"session":  session,
		"agent":    agent,
		"previous": previous,
	})
}

func runAGYTakeover(args []string) error {
	var agent, confirm string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--agent="):
			agent = strings.TrimPrefix(arg, "--agent=")
		case strings.HasPrefix(arg, "--confirm="):
			confirm = strings.TrimPrefix(arg, "--confirm=")
		default:
			return agyTakeoverUsageError()
		}
	}
	if strings.TrimSpace(agent) == "" {
		return agyTakeoverUsageError()
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	report, err := agy.Takeover(root, agent, confirm)
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, report)
}

func runAGYInstall(args []string) error {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		return agyInstallUsageError()
	}
	replace, err := replaceRequested(args[1:])
	if err != nil {
		return err
	}
	source, err := installationSource(install.Payload{
		Bundle:        adapterbundle.AGY,
		Parent:        "adapters",
		Name:          agy.AdapterName,
		ExecutableDir: "bin",
	})
	if err != nil {
		return err
	}
	source.Rewrite = agy.RewriteHookCommand
	result, err := install.Install(args[0], source, replace)
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, result)
}

func parseAGYHookFlags(args []string) (hook bool, session string, err error) {
	for _, arg := range args {
		switch {
		case arg == "--hook":
			hook = true
		case strings.HasPrefix(arg, sessionFlag):
			session = strings.TrimPrefix(arg, sessionFlag)
		default:
			return false, "", agyAdapterUsageError()
		}
	}
	return hook, session, nil
}

func agyAdapterUsageError() error {
	return errors.New("usage: conductor adapter agy <identity|stop> [--hook] [--session=<id>] | reside --session=<id> | release [--session=<id>] | bind <agent> [--session=<id>] | takeover --agent=<agent> [--confirm=<token>] | install <dir> [--replace]")
}

func agyInstallUsageError() error {
	return errors.New("usage: conductor adapter agy install <absolute-adapter-directory> [--replace]")
}

func agyBindUsageError() error {
	return errors.New("usage: conductor adapter agy bind <agent> [--session=<id>]")
}

func agyTakeoverUsageError() error {
	return errors.New("usage: conductor adapter agy takeover --agent=<agent> [--confirm=<token>]")
}

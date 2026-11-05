package main

import (
	"os"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/adapters/claude"
)

// wakeHost is an adapter that serves sessions on its host: its session-start
// hook records each one, and the host exports the session id to every command
// the model runs.
type wakeHost struct {
	binding     string
	environment string
}

var wakeHosts = []wakeHost{
	{binding: claude.BindingName, environment: claude.SessionEnvironment},
}

// bindWake answers join's wake verdict. A join from inside a session an
// adapter serves binds that session to the agent, so the adapter holds it from
// then on and joining is the agent's whole part; anywhere else the agent holds
// its own stream with watch.
func bindWake(root, agent string) (string, error) {
	for _, host := range wakeHosts {
		session := os.Getenv(host.environment)
		if session == "" {
			continue
		}
		served, err := binding.Served(root, host.binding, session)
		if err != nil {
			return "", err
		}
		if served {
			return "adapter", binding.Bind(root, host.binding, session, agent)
		}
	}
	return "watch", nil
}

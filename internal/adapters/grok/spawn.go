package grok

import (
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/binding"
)

// EnsureResident starts the detached resident when this session does not already have one.
func EnsureResident(root, session, project string) error {
	agent, err := Resolve(root, session, project)
	if err != nil || agent == "" {
		return err
	}
	if ResidentAlive(root, agent, session) {
		return nil
	}
	if err := clearFlag(root, "cancel", session); err != nil {
		return err
	}
	if err := clearFlag(root, "ready", session); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "adapter", "grok", "reside", "--session="+session)
	command.Dir = project
	command.Env = withConductorHome(os.Environ(), root)
	detach(command)
	if err := command.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ready, err := flagSet(root, "ready", session)
		if err != nil {
			return err
		}
		if ready && ResidentAlive(root, agent, session) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = setFlag(root, "cancel", session)
	_ = command.Process.Kill()
	return errors.New("grok resident did not acquire the guard")
}

func withConductorHome(env []string, root string) []string {
	filtered := env[:0:0]
	for _, item := range env {
		if len(item) < len("CONDUCTOR_HOME=") || item[:len("CONDUCTOR_HOME=")] != "CONDUCTOR_HOME=" {
			filtered = append(filtered, item)
		}
	}
	return append(filtered, "CONDUCTOR_HOME="+root)
}

// RecordHook records that this adapter serves the session. An empty or unsubstituted id fails.
func RecordHook(root, session, agent string) error {
	if err := binding.Validate(session, SessionPlaceholder); err != nil {
		return err
	}
	if agent == "" {
		return nil
	}
	return binding.Bind(root, AdapterName, session, agent)
}

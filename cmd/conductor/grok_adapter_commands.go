package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/adapters/grok"
	"github.com/danlavee/Conductor/internal/install"
	conductor "github.com/danlavee/Conductor/internal/state"
)

func runGrokAdapterCommand(args []string) error {
	if len(args) == 0 {
		return grokUsageError()
	}
	switch args[0] {
	case "install":
		return runGrokInstall(args[1:])
	case "bind":
		return runGrokBind(args[1:])
	case "identity", "reside", "stop", "release", "reannounce":
		return runGrokHook(args[0], args[1:])
	default:
		return grokUsageError()
	}
}

func runGrokHook(command string, args []string) error {
	session, err := grokHookSession(args)
	if err != nil {
		return err
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	project, err := grokProject()
	if err != nil {
		return err
	}
	switch command {
	case "reannounce":
		return grok.Reannounce(root, session)
	case "release":
		event, err := readGrokHookEvent()
		if err != nil {
			return err
		}
		return grok.Release(root, session, event.subagent() != "")
	case "identity":
		return runGrokIdentity(root, session, project)
	case "reside":
		return runGrokReside(root, session, project)
	case "stop":
		event, err := readGrokHookEvent()
		if err != nil {
			return err
		}
		return runGrokStop(root, session, project, event)
	default:
		return grokUsageError()
	}
}

// runGrokIdentity tells the session who it is, and whether the resident armed.
// A failure to arm is reported and still exits 0: the model then follows the skill.
func runGrokIdentity(root, session, project string) error {
	message := "Conductor adapter grok did not arm for this session."
	agent, err := grok.Resolve(root, session, project)
	if err != nil {
		message = "Conductor adapter grok did not arm. " + err.Error()
	} else if agent != "" {
		if err := grok.EnsureResident(root, session, project); err != nil {
			message = "Conductor adapter grok did not arm. " + err.Error()
		} else {
			message = grok.Announcement(session)
		}
	}
	return conductor.WriteJSON(os.Stdout, map[string]string{"systemMessage": message})
}

func runGrokReside(root, session, project string) error {
	agent, err := grok.Resolve(root, session, project)
	if err != nil || agent == "" {
		return err
	}
	client, err := conductor.Open(root, agent)
	if err != nil {
		return err
	}
	return grok.Reside(context.Background(), client, session)
}

func runGrokStop(root, session, project string, event grokHookEvent) error {
	input := grok.StopInput{
		Session:       session,
		Project:       project,
		Reason:        event.Reason,
		Subagent:      event.subagent(),
		LastAssistant: event.lastAssistant(),
	}
	restarted := false
	for {
		reason, announce, err := grok.Stop(context.Background(), root, input)
		if errors.Is(err, grok.ErrResidentDown) {
			if restarted {
				return nil
			}
			restarted = true
			if err := grok.EnsureResident(root, session, project); err != nil {
				if grokLocked(err) {
					return nil
				}
				return err
			}
			continue
		}
		if err != nil {
			if grokLocked(err) {
				return nil
			}
			return err
		}
		if reason == "" {
			return nil
		}
		if err := conductor.WriteJSON(os.Stdout, map[string]string{"decision": "block", "reason": reason}); err != nil {
			return err
		}
		if announce {
			return grok.ClearAnnouncement(root, session)
		}
		return nil
	}
}

func runGrokBind(args []string) error {
	agent, session, err := grokBindArgs(args)
	if err != nil {
		return err
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
	if err := grok.RecordHook(root, session, agent); err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, map[string]string{"adapter": "grok", "session": session, "agent": agent})
}

func grokBindArgs(args []string) (string, string, error) {
	var agent, session string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--session="):
			if session != "" {
				return "", "", grokBindUsageError()
			}
			session = strings.TrimPrefix(arg, "--session=")
		case agent == "" && !strings.HasPrefix(arg, "--"):
			agent = arg
		default:
			return "", "", grokBindUsageError()
		}
	}
	if strings.TrimSpace(agent) == "" {
		return "", "", grokBindUsageError()
	}
	if session == "" {
		session = os.Getenv(grok.SessionEnvironment)
	}
	if err := binding.Validate(session, grok.SessionPlaceholder); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(agent), session, nil
}

func runGrokInstall(args []string) error {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("usage: conductor adapter grok install <absolute-adapter-directory> [--replace]")
	}
	replace, err := replaceRequested(args[1:])
	if err != nil {
		return err
	}
	source, err := installationSource(install.Payload{
		Bundle:        adapterbundle.Grok,
		Parent:        "adapters",
		Name:          grok.AdapterName,
		ExecutableDir: "bin",
	})
	if err != nil {
		return err
	}
	source.Rewrite = grok.RewriteHookCommand
	result, err := install.Install(args[0], source, replace)
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, result)
}

func grokHookSession(args []string) (string, error) {
	if len(args) != 1 || !strings.HasPrefix(args[0], sessionFlag) {
		return "", grokUsageError()
	}
	session := strings.TrimPrefix(args[0], sessionFlag)
	if err := binding.Validate(session, grok.SessionPlaceholder); err != nil {
		return "", err
	}
	return session, nil
}

func grokProject() (string, error) {
	if project := strings.TrimSpace(os.Getenv("CLAUDE_PROJECT_DIR")); project != "" {
		return project, nil
	}
	return os.Getwd()
}

func grokLocked(err error) bool {
	var protocolErr *conductor.ProtocolError
	return errors.As(err, &protocolErr) && protocolErr.Code == "LOCKED"
}

type grokHookEvent struct {
	Reason               string `json:"reason"`
	SubagentType         string `json:"subagentType"`
	SubagentTypeSnake    string `json:"subagent_type"`
	LastAssistantMessage string `json:"lastAssistantMessage"`
	LastAssistantSnake   string `json:"last_assistant_message"`
}

func (e grokHookEvent) subagent() string {
	if e.SubagentType != "" {
		return e.SubagentType
	}
	return e.SubagentTypeSnake
}

func (e grokHookEvent) lastAssistant() string {
	if e.LastAssistantMessage != "" {
		return e.LastAssistantMessage
	}
	return e.LastAssistantSnake
}

func readGrokHookEvent() (grokHookEvent, error) {
	info, err := os.Stdin.Stat()
	if err == nil && info.Mode()&os.ModeCharDevice != 0 {
		return grokHookEvent{}, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return grokHookEvent{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return grokHookEvent{}, nil
	}
	var event grokHookEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return grokHookEvent{}, err
	}
	return event, nil
}

func grokUsageError() error {
	return errors.New("usage: conductor adapter grok <identity|reside|stop|release|reannounce> --session=<id> | bind <agent> [--session=<id>] | install <absolute-adapter-directory> [--replace]")
}

func grokBindUsageError() error {
	return errors.New("usage: conductor adapter grok bind <agent> [--session=<id>]")
}

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/codex"
	"github.com/danlavee/Conductor/internal/adapters/hold"
	"github.com/danlavee/Conductor/internal/install"
	conductor "github.com/danlavee/Conductor/internal/state"
)

var codexHost = adapterHost{
	binding: codex.AdapterName, environment: codex.ThreadEnvironment,
	payload: install.Payload{Bundle: adapterbundle.Codex, Parent: "adapters", Name: codex.AdapterName, ExecutableDir: "plugins/conductor/bin"},
	verbs:   []string{"identity", "arm", "inject", "release"}, input: "(hook event on stdin)",
	event: codexHookEvent, target: codex.HookTarget,
	serve: func(ctx context.Context, client *conductor.Client, session hold.Session) error {
		queue, err := codex.ResolveQueueHost()
		if err != nil {
			return err
		}
		return codex.Serve(ctx, client, session, queue)
	},
	inject: func(batch conductor.Batch) ([]conductor.ReadResult, error) {
		text, shown := conductor.RenderWake(batch)
		return shown, conductor.WriteJSON(os.Stdout, toolContext(text))
	},
	announce: func(agent string) error {
		const advice = "Conductor's Codex adapter serves this session. Join binds your name. Delivery is held while the native hook lives; after its timeout, messages wait until a later native lifecycle event restores it."
		if agent == "" {
			_, err := fmt.Fprintln(os.Stdout, advice+" No identity is bound yet.")
			return err
		}
		_, err := fmt.Fprintf(os.Stdout, "%s Current identity: %s.\n", advice, agent)
		return err
	},
}

func codexHookEvent(verb string, args []string, stdin io.Reader) (string, bool, error) {
	if len(args) != 0 {
		return "", false, errUsage
	}
	event, err := codex.ParseHookEvent(stdin)
	if err != nil {
		return "", false, err
	}
	valid := verb == "identity" && event.HookEventName == "SessionStart" ||
		verb == "release" && event.HookEventName == "SessionEnd" ||
		verb == "inject" && event.HookEventName == "PostToolUse" ||
		verb == "arm" && (event.HookEventName == "SessionStart" || event.HookEventName == "PostToolUse" || event.HookEventName == "Stop")
	if !valid {
		return "", false, fmt.Errorf("%s does not accept %s", verb, event.HookEventName)
	}
	return event.SessionID, event.AgentID == "", nil
}

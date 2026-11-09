package adapterbundle_test

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	adapterbundle "github.com/danlavee/Conductor/adapters"
)

func TestCodexBundleOwnsTheNativeHookLifecycles(t *testing.T) {
	for _, file := range []string{".agents/plugins/marketplace.json", "plugins/conductor/.codex-plugin/plugin.json"} {
		if _, err := fs.Stat(adapterbundle.Codex, file); err != nil {
			t.Fatal(err)
		}
	}
	data, err := fs.ReadFile(adapterbundle.Codex, "plugins/conductor/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command        string `json:"command"`
				CommandWindows string `json:"commandWindows"`
				Async          bool   `json:"async"`
				Timeout        int    `json:"timeout"`
				ContextLimit   *int   `json:"additionalContextLimit"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	for event, verbs := range map[string][]string{
		"SessionStart": {"identity", "arm"}, "PostToolUse": {"inject", "arm"},
		"Stop": {"arm"}, "SessionEnd": {"release"},
	} {
		groups := source.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != len(verbs) {
			t.Fatalf("%s registrations do not match %v", event, verbs)
		}
		for i, hook := range groups[0].Hooks {
			if verbs[i] == "inject" && (hook.ContextLimit == nil || *hook.ContextLimit != 0) {
				t.Error("inject must deliver the complete bounded wake rather than a native spill preview")
			}
			if !strings.Contains(hook.Command, "$PLUGIN_ROOT/bin/conductor") || !strings.HasSuffix(hook.Command, "codex "+verbs[i]) || !strings.Contains(hook.CommandWindows, "GetEnvironmentVariable('PLUGIN_ROOT')") {
				t.Errorf("%s %s runs outside its installed plugin root", event, verbs[i])
			}
			if hook.Async != (verbs[i] == "arm") || verbs[i] == "arm" && hook.Timeout != 86400 || event == "SessionEnd" && hook.Timeout > 3 {
				t.Errorf("%s %s has the wrong native lifetime", event, verbs[i])
			}
		}
	}
}

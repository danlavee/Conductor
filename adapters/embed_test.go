package adapterbundle_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"testing"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/claude"
)

// TestEmbeddedAdapterCarriesWhatTheHostLoads guards the two files the host
// reads and the one directory an ordinary embed directive would have dropped
// for beginning with a dot. Losing any of them produces an installation that
// places files successfully and is never loaded, which is the failure this
// whole branch exists to stop shipping quietly.
func TestEmbeddedAdapterCarriesWhatTheHostLoads(t *testing.T) {
	for _, required := range []string{
		".claude-plugin/plugin.json",
		".claude-plugin/marketplace.json",
		"hooks/hooks.json",
	} {
		if _, err := fs.Stat(adapterbundle.ClaudeCode, required); err != nil {
			t.Errorf("embedded adapter is missing %s: %v", required, err)
		}
	}
	if _, err := fs.Stat(adapterbundle.ClaudeCode, "embed.go"); !os.IsNotExist(err) {
		t.Error("embedding source appears in the portable adapter")
	}
}

// TestEmbeddedHooksNameTheInstalledExecutable ties the hook registrations to
// where the installer actually puts the binary. They are written in different
// files, in different languages, by different concerns -- and if they drift the
// adapter installs cleanly and every hook fails to spawn.
func TestEmbeddedHooksNameTheInstalledExecutable(t *testing.T) {
	data, err := fs.ReadFile(adapterbundle.ClaudeCode, "hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var registrations map[string][]struct {
		Hooks []struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &registrations); err != nil {
		t.Fatal(err)
	}
	commands := map[string]bool{}
	for event, matchers := range registrations {
		for _, matcher := range matchers {
			for _, hook := range matcher.Hooks {
				if hook.Command != "${CLAUDE_PLUGIN_ROOT}/bin/conductor" {
					t.Errorf("%s hook runs %q, which is not where the installer places the executable", event, hook.Command)
				}
				// Args present means the host spawns the command directly
				// rather than through a shell, which is the property that
				// lets one registration cover every platform.
				if len(hook.Args) < 3 || hook.Args[0] != "adapter" || hook.Args[1] != "claude" {
					t.Errorf("%s hook args = %v", event, hook.Args)
					continue
				}
				commands[hook.Args[2]] = true
			}
		}
	}
	for _, responsibility := range []string{"identity", "arm", "release"} {
		if !commands[responsibility] {
			t.Errorf("no hook runs %q", responsibility)
		}
	}
}

// TestAdapterNameMatchesTheEmbeddedDirectory keeps the installed directory and
// the embedded tree spelled the same. The constant decides where the payload
// lands; the directory decides what lands there.
func TestAdapterNameMatchesTheEmbeddedDirectory(t *testing.T) {
	if _, err := os.Stat(claude.AdapterName); err != nil {
		t.Fatalf("AdapterName %q names no directory beside the embedding: %v", claude.AdapterName, err)
	}
}

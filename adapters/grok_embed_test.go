package adapterbundle_test

import (
	"bytes"
	"io/fs"
	"testing"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/grok"
	"github.com/danlavee/Conductor/internal/install"
)

func TestEmbeddedGrokAdapterCarriesWhatTheHostLoads(t *testing.T) {
	for _, required := range []string{".grok-plugin/marketplace.json", "plugin.json", "hooks/hooks.json"} {
		if _, err := fs.Stat(adapterbundle.Grok, required); err != nil {
			t.Errorf("embedded grok adapter is missing %s: %v", required, err)
		}
	}
	data, err := fs.ReadFile(adapterbundle.Grok, "hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(install.HookCommandPlaceholder)) || !bytes.Contains(data, []byte("--session="+grok.SessionPlaceholder)) {
		t.Fatalf("grok hooks do not name the placeholder executable and session: %s", data)
	}
}

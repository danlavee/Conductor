package adapterbundle_test

import (
	"bytes"
	"io/fs"
	"testing"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/install"
)

func TestEmbeddedAGYAdapterCarriesWhatTheHostLoads(t *testing.T) {
	for _, required := range []string{"plugin.json", "hooks.json", "README.md"} {
		if _, err := fs.Stat(adapterbundle.AGY, required); err != nil {
			t.Errorf("embedded agy adapter is missing %s: %v", required, err)
		}
	}
	data, err := fs.ReadFile(adapterbundle.AGY, "hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(install.HookCommandPlaceholder)) {
		t.Fatalf("agy hooks do not name the placeholder executable: %s", data)
	}
}

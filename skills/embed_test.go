package skillbundle_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	skillbundle "github.com/danlavee/Conductor/skills"
)

// TestEmbeddedSkillMatchesCanonicalTree pins what install places to the
// skill as written: every file under conductor/ is embedded byte for byte,
// and nothing else is.
func TestEmbeddedSkillMatchesCanonicalTree(t *testing.T) {
	source := os.DirFS("conductor")
	for _, pair := range [][2]fs.FS{{source, skillbundle.Files}, {skillbundle.Files, source}} {
		err := fs.WalkDir(pair[0], ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			want, _ := fs.ReadFile(pair[0], name)
			got, err := fs.ReadFile(pair[1], name)
			if err != nil || !bytes.Equal(want, got) {
				t.Errorf("%s differs between the skill tree and its embedding", filepath.FromSlash(name))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

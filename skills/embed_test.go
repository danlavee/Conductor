package skillbundle_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danlavee/Conductor/internal/skillcheck"
	skillbundle "github.com/danlavee/Conductor/skills"
)

func TestEmbeddedSkillMatchesCanonicalTree(t *testing.T) {
	if problems := skillcheck.ValidateSkill(skillbundle.Files); len(problems) > 0 {
		t.Fatalf("embedded skill is invalid: %v", problems)
	}
	if _, err := fs.Stat(skillbundle.Files, "embed.go"); !os.IsNotExist(err) {
		t.Fatal("embedding source appears in the portable skill")
	}
	err := filepath.WalkDir("conductor", func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel("conductor", sourcePath)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.ToSlash(relative), "scripts/") {
			return nil
		}
		sourceData, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		embeddedData, err := fs.ReadFile(skillbundle.Files, filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		if !bytes.Equal(sourceData, embeddedData) {
			t.Errorf("embedded bytes differ for %s", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	commandPattern = regexp.MustCompile("conductor[ \\t]+(?:<[a-z][a-z-]*>[ \\t]+)?([a-z][a-z0-9-]*)")
	codePattern    = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")
	linkPattern    = regexp.MustCompile(`\[[^]]+\]\(([^)#]*)[^)]*\)`)
)

// TestDocsNameRealCommandsAndResolveTheirLinks holds every Markdown file to
// the CLI it describes: a command shown in code is one usageText offers, a
// relative link lands on a file, and the skill links nowhere outside itself,
// because only the skill is installed.
func TestDocsNameRealCommandsAndResolveTheirLinks(t *testing.T) {
	commands := map[string]bool{}
	for _, match := range commandPattern.FindAllStringSubmatch(usageText, -1) {
		commands[match[1]] = true
	}
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	skill := filepath.Join(root, "skills", "conductor")
	if data, err := os.ReadFile(filepath.Join(skill, "SKILL.md")); err != nil || !regexp.MustCompile(`\A---\r?\nname: conductor\r?\ndescription: .+\r?\n---\r?\n`).Match(data) {
		t.Errorf("SKILL.md frontmatter must be name: conductor and a description: %v", err)
	}
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && file != root && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		if filepath.Ext(file) != ".md" {
			return nil
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, file)
		for _, code := range codePattern.FindAllString(string(data), -1) {
			for _, match := range commandPattern.FindAllStringSubmatch(code, -1) {
				if !commands[match[1]] {
					t.Errorf("%s shows command %q, which the CLI does not have", relative, match[1])
				}
			}
		}
		for _, match := range linkPattern.FindAllStringSubmatch(string(data), -1) {
			if match[1] == "" || strings.Contains(match[1], "://") || strings.HasPrefix(match[1], "mailto:") {
				continue
			}
			target := filepath.Join(filepath.Dir(file), filepath.FromSlash(match[1]))
			if _, err := os.Stat(target); err != nil {
				t.Errorf("%s links to missing %s", relative, match[1])
			}
			if strings.HasPrefix(file, skill+string(filepath.Separator)) && !strings.HasPrefix(target, skill+string(filepath.Separator)) {
				t.Errorf("%s links outside the installed skill: %s", relative, match[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

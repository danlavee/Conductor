// Package skillcheck validates the repository's distributable skill contract.
package skillcheck

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var requiredRepositoryFiles = []string{
	"README.md",
	"go.mod",
	"cmd/conductor/main.go",
	"cmd/skillcheck/main.go",
	"internal/skillcheck/check.go",
	"docs/use-case.md",
	"docs/use-cases/README.md",
}

var requiredSkillFiles = []string{
	"SKILL.md",
	"references/protocol.md",
	"references/limitations.md",
}

var supportedCommands = map[string]bool{
	"register": true, "deregister": true, "list-agents": true,
	"begin": true, "commit": true, "abort": true, "put": true, "edit": true, "strike": true,
	"get": true, "watch": true, "install": true, "version": true,
	"channel": true,
}

var (
	commandPattern = regexp.MustCompile("(?m)(?:^|`)[ \\t]*conductor[ \\t]+([a-z][a-z0-9-]*)")
	linkPattern    = regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	frontmatter    = regexp.MustCompile(`(?s)\A---\r?\nname: conductor\r?\ndescription: .+?\r?\n---\r?\n`)
)

// Validate returns every structural problem found under the repository root.
func Validate(root string) []string {
	var problems []string
	for _, relative := range requiredRepositoryFiles {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil || info.IsDir() {
			problems = append(problems, "missing required file: "+relative)
		}
	}
	for _, problem := range ValidateSkill(os.DirFS(filepath.Join(root, "skills", "conductor"))) {
		problems = append(problems, "skills/conductor/"+problem)
	}

	_ = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		slash := filepath.ToSlash(relative)
		if info.IsDir() && (info.Name() == "__pycache__" || info.Name() == ".local") {
			return filepath.SkipDir
		}
		if info.IsDir() && slash == "skills/conductor" {
			return filepath.SkipDir
		}
		if info.IsDir() || strings.Contains(slash, "/.local/") || strings.HasPrefix(slash, ".local/") {
			return nil
		}
		if strings.Contains(info.Name(), "_v2") {
			problems = append(problems, "version-suffixed duplicate remains: "+slash)
		}
		if filepath.Ext(info.Name()) == ".py" || filepath.Ext(info.Name()) == ".pyc" {
			problems = append(problems, "Python implementation remains: "+slash)
		}
		if filepath.Ext(info.Name()) != ".md" && slash != "README.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, "unreadable file: "+slash)
			return nil
		}
		text := string(data)
		for _, match := range commandPattern.FindAllStringSubmatch(text, -1) {
			if !supportedCommands[match[1]] {
				problems = append(problems, "unsupported command "+match[1]+" in "+slash)
			}
		}
		for _, match := range linkPattern.FindAllStringSubmatch(text, -1) {
			target := strings.SplitN(match[1], "#", 2)[0]
			if target == "" || strings.Contains(target, "://") {
				continue
			}
			destination := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(target)))
			if _, err := os.Stat(destination); err != nil {
				problems = append(problems, "broken link "+match[1]+" in "+slash)
			}
		}
		return nil
	})

	sort.Strings(problems)
	return problems
}

// ValidateSkill returns structural and reference problems in a portable skill filesystem.
func ValidateSkill(skill fs.FS) []string {
	var problems []string
	for _, required := range requiredSkillFiles {
		if info, err := fs.Stat(skill, required); err != nil || info.IsDir() {
			problems = append(problems, "missing required file: "+required)
		}
	}
	if data, err := fs.ReadFile(skill, "SKILL.md"); err == nil && !frontmatter.Match(data) {
		problems = append(problems, "SKILL.md has invalid frontmatter")
	}

	_ = fs.WalkDir(skill, ".", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			problems = append(problems, "unreadable path: "+filePath)
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if strings.Contains(entry.Name(), "_v2") {
			problems = append(problems, "version-suffixed duplicate remains: "+filePath)
		}
		extension := path.Ext(entry.Name())
		if extension == ".py" || extension == ".pyc" {
			problems = append(problems, "Python implementation remains: "+filePath)
		}
		if extension == ".json" && strings.HasPrefix(filePath, "assets/") {
			data, err := fs.ReadFile(skill, filePath)
			if err != nil || !json.Valid(data) {
				problems = append(problems, "invalid JSON asset: "+filePath)
			}
		}
		if extension != ".md" {
			return nil
		}
		data, err := fs.ReadFile(skill, filePath)
		if err != nil {
			problems = append(problems, "unreadable file: "+filePath)
			return nil
		}
		text := string(data)
		for _, match := range commandPattern.FindAllStringSubmatch(text, -1) {
			if !supportedCommands[match[1]] {
				problems = append(problems, "unsupported command "+match[1]+" in "+filePath)
			}
		}
		for _, match := range linkPattern.FindAllStringSubmatch(text, -1) {
			target := strings.SplitN(match[1], "#", 2)[0]
			if target == "" || strings.Contains(target, "://") {
				continue
			}
			destination := path.Clean(path.Join(path.Dir(filePath), target))
			if destination == ".." || strings.HasPrefix(destination, "../") || strings.HasPrefix(destination, "/") {
				problems = append(problems, "escaping link "+match[1]+" in "+filePath)
				continue
			}
			if _, err := fs.Stat(skill, destination); err != nil {
				problems = append(problems, "broken link "+match[1]+" in "+filePath)
			}
		}
		return nil
	})
	sort.Strings(problems)
	return problems
}

// FindRoot walks upward from the working directory to the Go module root.
func FindRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

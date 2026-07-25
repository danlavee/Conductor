// Package skillcheck validates the repository's distributable skill contract.
package skillcheck

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

// supportedCommands is ValidateSkill's copy of cmd/conductor/main.go's
// command verbs. It exists only because ValidateSkill also runs at install
// time against a bundled skill with no Go source available to read the real
// dispatch from -- Validate cross-checks this against cliCommands so drift
// fails a test instead of silently doing nothing.
var supportedCommands = map[string]bool{
	"join": true, "leave": true, "list-agents": true, "subscribe": true, "list": true,
	"begin": true, "commit": true, "abort": true, "put": true, "edit": true, "strike": true,
	"redact": true, "get": true, "watch": true, "install": true, "version": true, "migrate": true,
}

var (
	// commandPattern matches a verb after "conductor", optionally skipping a
	// single <placeholder> token (e.g. <agent>) -- covers both
	// "conductor <agent> join" and standalone "conductor install".
	commandPattern = regexp.MustCompile("(?m)(?:^|`)[ \\t]*conductor[ \\t]+(?:<[a-z][a-z-]*>[ \\t]+)?([a-z][a-z0-9-]*)")
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

	relevant, err := relevantFiles(root)
	if err != nil {
		problems = append(problems, "list repository files: "+err.Error())
		sort.Strings(problems)
		return problems
	}
	commands, err := cliCommands(root)
	if err != nil {
		problems = append(problems, "list CLI commands: "+err.Error())
		sort.Strings(problems)
		return problems
	}
	for verb := range commands {
		if !supportedCommands[verb] {
			problems = append(problems, "supportedCommands is missing CLI verb: "+verb)
		}
	}
	for verb := range supportedCommands {
		if !commands[verb] {
			problems = append(problems, "supportedCommands has a verb main.go no longer dispatches: "+verb)
		}
	}

	for _, slash := range relevant {
		if slash == "skills/conductor" || strings.HasPrefix(slash, "skills/conductor/") {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(slash))
		name := filepath.Base(slash)
		if strings.Contains(name, "_v2") {
			problems = append(problems, "version-suffixed duplicate remains: "+slash)
		}
		if filepath.Ext(name) == ".py" || filepath.Ext(name) == ".pyc" {
			problems = append(problems, "Python implementation remains: "+slash)
		}
		if filepath.Ext(name) != ".md" && slash != "README.md" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, "unreadable file: "+slash)
			continue
		}
		text := string(data)
		for _, match := range commandPattern.FindAllStringSubmatch(text, -1) {
			if !commands[match[1]] {
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
	}

	sort.Strings(problems)
	return problems
}

// cliCommands returns every command verb cmd/conductor/main.go actually
// dispatches on, read directly from its two top-level switch statements
// (switch args[0] for standalone commands, switch command for agent-scoped
// ones) rather than a hand-maintained copy -- so this list cannot silently
// drift from what the CLI really accepts, the way a separate list did.
func cliCommands(root string) (map[string]bool, error) {
	mainPath := filepath.Join(root, "cmd", "conductor", "main.go")
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, mainPath, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", mainPath, err)
	}
	commands := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		statement, ok := node.(*ast.SwitchStmt)
		if !ok || !isDispatchSwitch(statement.Tag) {
			return true
		}
		for _, clause := range statement.Body.List {
			caseClause, ok := clause.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expression := range caseClause.List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				if value, err := strconv.Unquote(literal.Value); err == nil {
					commands[value] = true
				}
			}
		}
		return true
	})
	if len(commands) == 0 {
		return nil, fmt.Errorf("no command switch found in %s", mainPath)
	}
	return commands, nil
}

// isDispatchSwitch reports whether tag is "command" or "args[0]" -- run()'s
// two dispatch points -- as opposed to unrelated switches in the same file
// (the watch --claude-cli sub-flag, migrate's protocol-version dispatch).
func isDispatchSwitch(tag ast.Expr) bool {
	if ident, ok := tag.(*ast.Ident); ok {
		return ident.Name == "command"
	}
	index, ok := tag.(*ast.IndexExpr)
	if !ok {
		return false
	}
	ident, ok := index.X.(*ast.Ident)
	if !ok || ident.Name != "args" {
		return false
	}
	literal, ok := index.Index.(*ast.BasicLit)
	return ok && literal.Value == "0"
}

// relevantFiles returns every file under root that git wouldn't ignore --
// tracked or untracked, but never a .gitignore'd path -- relative to root
// and slash-separated. .gitignore is the single source of truth for what
// this validator skips, so nothing here is hardcoded.
func relevantFiles(root string) ([]string, error) {
	output, err := exec.Command("git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
		if line != "" {
			files = append(files, filepath.ToSlash(line))
		}
	}
	return files, nil
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

package skillcheck

import (
	"testing"
	"testing/fstest"
)

func TestRepositoryPackageIsValid(t *testing.T) {
	root, err := FindRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range Validate(root) {
		t.Error(problem)
	}
}

func TestValidateSkillRejectsEscapingLinksAndInvalidAssets(t *testing.T) {
	skill := fstest.MapFS{
		"SKILL.md":                                  &fstest.MapFile{Data: []byte("---\r\nname: conductor\r\ndescription: Test.\r\n---\r\n\r\n[Escape](../outside.md)\r\n")},
		"references/limitations.md":                      &fstest.MapFile{Data: []byte("# Limits\n")},
		"references/protocol.md":                    &fstest.MapFile{Data: []byte("# Protocol\n")},
		"references/integrations/README.md":         &fstest.MapFile{Data: []byte("# Integrations\n")},
		"references/integrations/codex.md":          &fstest.MapFile{Data: []byte("# Codex\n")},
		"references/integrations/agy.md":            &fstest.MapFile{Data: []byte("# Antigravity\n")},
		"references/integrations/claude-cli.md":     &fstest.MapFile{Data: []byte("# Claude CLI\n")},
		"references/integrations/claude-channel.md": &fstest.MapFile{Data: []byte("# Claude channel\n")},
		"references/integrations/claude-desktop.md": &fstest.MapFile{Data: []byte("# Claude Desktop\n")},
		"assets/example.json":                       &fstest.MapFile{Data: []byte("not json")},
	}
	problems := ValidateSkill(skill)
	if len(problems) != 2 {
		t.Fatalf("problems = %v", problems)
	}
}

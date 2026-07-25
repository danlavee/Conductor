package install

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

const smokeHelperEnvironment = "CONDUCTOR_INSTALL_SMOKE_HELPER"
const smokeProtocolEnvironment = "CONDUCTOR_INSTALL_SMOKE_PROTOCOL"

func TestMain(m *testing.M) {
	if os.Getenv(smokeHelperEnvironment) == "1" && len(os.Args) == 2 && os.Args[1] == "version" {
		protocol := os.Getenv(smokeProtocolEnvironment)
		if protocol == "" {
			protocol = "1"
		}
		fmt.Printf("{\"version\":\"v1.2.3\",\"protocol\":%s}\n", protocol)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInstallRequiresExactAbsoluteSkillDirectory(t *testing.T) {
	source := testSource(t)
	validParent := filepath.Join(t.TempDir(), ".agents", "skills")
	for name, destination := range map[string]string{
		"missing":    "",
		"relative":   filepath.Join(".agents", "skills", "conductor"),
		"wrong name": filepath.Join(validParent, "other"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Install(destination, source); err == nil {
				t.Fatal("invalid destination was accepted")
			}
		})
	}
}

func TestWindowsDestinationDistinguishesExtendedLocalAndUNC(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	extendedLocal := `\\?\` + testDestination(t)
	if err := ValidateDestination(extendedLocal); err != nil {
		t.Fatalf("extended local path rejected: %v", err)
	}
	if err := ValidateDestination(`\\server\share\.agents\skills\conductor`); err == nil {
		t.Fatal("UNC destination was accepted")
	}
}

func TestInstallPublishesCompleteIdempotentSkill(t *testing.T) {
	destination := testDestination(t)
	source := testSource(t)
	result, err := Install(destination, source)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "installed" || result.Protocol != 1 || result.SkillPath != destination || result.FileCount != 12 {
		t.Fatalf("unexpected result: %+v", result)
	}
	for _, relative := range []string{
		"SKILL.md",
		"references/limitations.md",
		"references/protocol.md",
		"references/guide.md",
		"references/integrations/README.md",
		"references/integrations/codex.md",
		"references/integrations/agy.md",
		"references/integrations/claude-cli.md",
		"references/integrations/claude-channel.md",
		"references/integrations/claude-desktop.md",
		"assets/nested/example.txt",
		executableRelativePath(runtime.GOOS),
		manifestName,
	} {
		if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(relative))); err != nil {
			t.Errorf("missing %s: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "embed.go")); !os.IsNotExist(err) {
		t.Fatal("Go source was installed")
	}
	manifestPath := filepath.Join(destination, manifestName)
	fixedTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(manifestPath, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	manifestInfo, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Install(destination, source)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != "already-installed" || again.ManifestHash != result.ManifestHash {
		t.Fatalf("unexpected idempotent result: %+v", again)
	}
	afterInfo, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !afterInfo.ModTime().Equal(manifestInfo.ModTime()) {
		t.Fatal("idempotent install rewrote the destination")
	}
}

func TestInstallRejectsTamperedAndExtraFilesWithoutRepair(t *testing.T) {
	for name, mutate := range map[string]func(string) error{
		"tampered": func(destination string) error {
			return os.WriteFile(filepath.Join(destination, "SKILL.md"), []byte("changed"), 0o644)
		},
		"extra": func(destination string) error {
			return os.WriteFile(filepath.Join(destination, "extra.txt"), []byte("extra"), 0o644)
		},
		"missing": func(destination string) error {
			return os.Remove(filepath.Join(destination, "references", "limitations.md"))
		},
		"empty directory": func(destination string) error {
			return os.Mkdir(filepath.Join(destination, "unexpected"), 0o755)
		},
	} {
		t.Run(name, func(t *testing.T) {
			destination := testDestination(t)
			source := testSource(t)
			if _, err := Install(destination, source); err != nil {
				t.Fatal(err)
			}
			if err := mutate(destination); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(destination, source); err == nil || !strings.Contains(err.Error(), "install conflict") {
				t.Fatalf("error = %v, want install conflict", err)
			}
		})
	}
}

func TestInstallRunsStagedExecutableSmokeCheck(t *testing.T) {
	t.Setenv(smokeHelperEnvironment, "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source := testSource(t)
	source.ExecutablePath = executable
	source.SmokeCheck = true
	if _, err := Install(testDestination(t), source); err != nil {
		t.Fatal(err)
	}
}

func TestInstallCleansStageAfterSmokeFailure(t *testing.T) {
	destination := testDestination(t)
	source := testSource(t)
	source.SmokeCheck = true
	if _, err := Install(destination, source); err == nil || !strings.Contains(err.Error(), "install verify") {
		t.Fatalf("error = %v, want smoke-check failure", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failure: %v", err)
	}
	matches := stagingMatches(t, destination)
	if len(matches) != 0 {
		t.Fatalf("staging directories remain: %v", matches)
	}
}

func TestInstallRejectsStagedProtocolMismatch(t *testing.T) {
	t.Setenv(smokeHelperEnvironment, "1")
	t.Setenv(smokeProtocolEnvironment, "2")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source := testSource(t)
	source.ExecutablePath = executable
	source.SmokeCheck = true
	if _, err := Install(testDestination(t), source); err == nil || !strings.Contains(err.Error(), "protocol 2 does not match 1") {
		t.Fatalf("error = %v, want protocol mismatch", err)
	}
}

func TestInstallRequiresPositiveProtocol(t *testing.T) {
	source := testSource(t)
	source.Protocol = 0
	if _, err := Install(testDestination(t), source); err == nil || !strings.Contains(err.Error(), "protocol version must be positive") {
		t.Fatalf("error = %v, want invalid protocol", err)
	}
}

func TestInstallRejectsLinkToFilteredGoSource(t *testing.T) {
	destination := testDestination(t)
	bundle := testBundle().(fstest.MapFS)
	bundle["SKILL.md"] = &fstest.MapFile{Data: []byte("---\nname: conductor\ndescription: Test Conductor.\n---\n\n[Guide](references/guide.md)\n[Helper](helper.go)\n")}
	bundle["helper.go"] = &fstest.MapFile{Data: []byte("package helper\n")}
	source := testSource(t)
	source.Bundle = bundle
	if _, err := Install(destination, source); err == nil || !strings.Contains(err.Error(), "invalid staged skill") {
		t.Fatalf("error = %v, want filtered-link failure", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failure: %v", err)
	}
	if matches := stagingMatches(t, destination); len(matches) != 0 {
		t.Fatalf("staging directories remain: %v", matches)
	}
}

func TestConcurrentInstallPublishesOnce(t *testing.T) {
	destination := testDestination(t)
	source := testSource(t)
	start := make(chan struct{})
	results := make(chan string, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := Install(destination, source)
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- result.Status
		}()
	}
	close(start)
	group.Wait()
	close(results)
	var statuses []string
	for status := range results {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	if fmt.Sprint(statuses) != "[already-installed installed]" {
		t.Fatalf("statuses = %v", statuses)
	}
	matches := stagingMatches(t, destination)
	if len(matches) != 0 {
		t.Fatalf("staging directories remain: %v", matches)
	}
}

func stagingMatches(t *testing.T, destination string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(destination)), ".conductor-install-*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestManifestIsIndependentOfDestination(t *testing.T) {
	source := testSource(t)
	first := testDestination(t)
	second := testDestination(t)
	if _, err := Install(first, source); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(second, source); err != nil {
		t.Fatal(err)
	}
	firstManifest, err := os.ReadFile(filepath.Join(first, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	secondManifest, err := os.ReadFile(filepath.Join(second, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstManifest, secondManifest) {
		t.Fatal("manifest depends on destination")
	}
}

func TestLinuxIdempotencyRequiresOwnerExecute(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux mode check")
	}
	destination := testDestination(t)
	source := testSource(t)
	if _, err := Install(destination, source); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(destination, filepath.FromSlash(executableRelativePath(runtime.GOOS)))
	if err := os.Chmod(executable, 0o001); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(destination, source); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("error = %v, want executable-mode conflict", err)
	}
}

func TestInstallRejectsExistingDirectoryAndSymlink(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			destination := testDestination(t)
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				t.Fatal(err)
			}
			if kind == "directory" {
				if err := os.Mkdir(destination, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				target := t.TempDir()
				if err := os.Symlink(target, destination); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if _, err := Install(destination, testSource(t)); err == nil {
				t.Fatal("existing conflicting destination was accepted")
			}
		})
	}
}

func TestReleasedDistributionIdentityIgnoresCompilerBytes(t *testing.T) {
	first := []payloadFile{{manifestFile: manifestFile{Path: "SKILL.md", SHA256: digest([]byte("skill"))}}}
	bundleDigest := digestPayload(first)
	one := distributionIdentity(manifestFormat, 1, "v1.2.3", runtime.GOOS, runtime.GOARCH, bundleDigest, digest([]byte("binary one")))
	two := distributionIdentity(manifestFormat, 1, "v1.2.3", runtime.GOOS, runtime.GOARCH, bundleDigest, digest([]byte("binary two")))
	if one != two {
		t.Fatal("released identity depends on compiler-produced bytes")
	}
	develOne := distributionIdentity(manifestFormat, 1, "(devel)", runtime.GOOS, runtime.GOARCH, bundleDigest, digest([]byte("binary one")))
	develTwo := distributionIdentity(manifestFormat, 1, "(devel)", runtime.GOOS, runtime.GOARCH, bundleDigest, digest([]byte("binary two")))
	if develOne == develTwo {
		t.Fatal("development identity ignores executable bytes")
	}
}

func TestDistributionIdentityIncludesProtocolAndFormat(t *testing.T) {
	bundle := digest([]byte("bundle"))
	binary := digest([]byte("binary"))
	base := distributionIdentity(manifestFormat, 1, "v1.2.3", runtime.GOOS, runtime.GOARCH, bundle, binary)
	if base == distributionIdentity(manifestFormat, 2, "v1.2.3", runtime.GOOS, runtime.GOARCH, bundle, binary) {
		t.Fatal("distribution identity ignores protocol")
	}
	if base == distributionIdentity(manifestFormat+1, 1, "v1.2.3", runtime.GOOS, runtime.GOARCH, bundle, binary) {
		t.Fatal("distribution identity ignores manifest format")
	}
}

func testDestination(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".agents", "skills", "conductor")
}

func testSource(t *testing.T) Source {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "conductor-test")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if err := os.WriteFile(executable, []byte("native executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Source{
		Bundle:         testBundle(),
		ExecutablePath: executable,
		Version:        "v1.2.3",
		Protocol:       1,
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		SmokeCheck:     false,
	}
}

func TestCheckCurrencyReportsNotInstalledWithoutWriting(t *testing.T) {
	destination := testDestination(t)
	source := testSource(t)
	result, err := CheckCurrency(destination, source)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "not-installed" || result.SourceVersion != source.Version {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("CheckCurrency wrote to destination")
	}
}

func TestCheckCurrencyReportsCurrentAfterMatchingInstall(t *testing.T) {
	destination := testDestination(t)
	source := testSource(t)
	if _, err := Install(destination, source); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(destination, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	result, err := CheckCurrency(destination, source)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "current" || result.InstalledVersion != source.Version || result.SourceVersion != source.Version {
		t.Fatalf("unexpected result: %+v", result)
	}
	after, err := os.ReadFile(filepath.Join(destination, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("CheckCurrency modified the installed manifest")
	}
}

func TestCheckCurrencyReportsOutdatedForDifferentSource(t *testing.T) {
	destination := testDestination(t)
	original := testSource(t)
	if _, err := Install(destination, original); err != nil {
		t.Fatal(err)
	}
	newer := original
	newer.Version = "v9.9.9"
	result, err := CheckCurrency(destination, newer)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "outdated" || result.InstalledVersion != original.Version || result.SourceVersion != newer.Version {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCheckCurrencyReportsUnknownForMissingManifest(t *testing.T) {
	destination := testDestination(t)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "stray.txt"), []byte("hand-copied"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := CheckCurrency(destination, testSource(t))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unknown" || result.Detail == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func testBundle() fs.FS {
	return fstest.MapFS{
		"SKILL.md":                                  &fstest.MapFile{Data: []byte("---\nname: conductor\ndescription: Test Conductor.\n---\n\n[Guide](references/guide.md)\n")},
		"references/limitations.md":                      &fstest.MapFile{Data: []byte("# Limits\n")},
		"references/protocol.md":                    &fstest.MapFile{Data: []byte("# Protocol\n\n[Skill](../SKILL.md)\n")},
		"references/guide.md":                       &fstest.MapFile{Data: []byte("# Guide\n")},
		"references/integrations/README.md":         &fstest.MapFile{Data: []byte("# Integrations\n")},
		"references/integrations/codex.md":          &fstest.MapFile{Data: []byte("# Codex\n")},
		"references/integrations/agy.md":            &fstest.MapFile{Data: []byte("# Antigravity\n")},
		"references/integrations/claude-cli.md":     &fstest.MapFile{Data: []byte("# Claude CLI\n")},
		"references/integrations/claude-channel.md": &fstest.MapFile{Data: []byte("# Claude channel\n")},
		"references/integrations/claude-desktop.md": &fstest.MapFile{Data: []byte("# Claude Desktop\n")},
		"assets/nested/example.txt":                 &fstest.MapFile{Data: []byte("asset\n")},
		"embed.go":                                  &fstest.MapFile{Data: []byte("package ignored\n")},
	}
}

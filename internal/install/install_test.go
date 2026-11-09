package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// The test binary stands in for the executable being installed, so the smoke
// check runs a real process. It answers `version` with this protocol.
const smokeProtocolEnvironment = "CONDUCTOR_INSTALL_SMOKE_PROTOCOL"

func TestMain(m *testing.M) {
	if protocol := os.Getenv(smokeProtocolEnvironment); protocol != "" && len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Printf("{\"version\":\"v1.2.3\",\"protocol\":%s}\n", protocol)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testSource(t *testing.T, skillText string) Source {
	t.Helper()
	t.Setenv(smokeProtocolEnvironment, "1")
	bundle := fstest.MapFS{
		"SKILL.md":               {Data: []byte(skillText)},
		"references/protocol.md": {Data: []byte("# Protocol\n")},
		"plugin.json":            {Data: []byte(`{"version":"` + VersionPlaceholder + `"}`)},
	}
	return Source{
		Payload:    Payload{Bundle: bundle, Parent: "skills", Name: "conductor", ExecutableDir: "scripts"},
		Executable: os.Args[0],
		Version:    "v1.2.3",
		Protocol:   1,
	}
}

// testDestination also points the backup directory at a temp home, so no test
// writes under the real ~/.conductor.
func testDestination(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return filepath.Join(t.TempDir(), ".agents", "skills", "conductor")
}

func status(t *testing.T, destination string, source Source) string {
	t.Helper()
	currency, err := CheckCurrency(destination, source)
	if err != nil {
		t.Fatal(err)
	}
	return currency.Status
}

func TestDestinationMustBeAbsoluteAndEndInParentAndName(t *testing.T) {
	source := testSource(t, "skill\n")
	for _, destination := range []string{"", filepath.Join("skills", "conductor"), filepath.Join(t.TempDir(), "skills", "other"), filepath.Join(t.TempDir(), "plugins", "conductor")} {
		if _, err := Install(destination, source, false); err == nil {
			t.Fatalf("destination %q was accepted", destination)
		}
	}
}

func TestInstallIsIdempotentAndRunnable(t *testing.T) {
	destination := testDestination(t)
	source := testSource(t, "skill\n")
	if got := status(t, destination, source); got != "not-installed" {
		t.Fatalf("status = %s", got)
	}
	// A trailing separator, as shell completion leaves it, names the same tree.
	first, err := Install(destination+string(filepath.Separator), source, false)
	if err != nil || first.Status != "installed" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	if first.BinaryPath != filepath.Join(destination, filepath.FromSlash(source.executable())) {
		t.Fatalf("binary at %s", first.BinaryPath)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "references", "protocol.md")); err != nil || string(data) != "# Protocol\n" {
		t.Fatalf("payload file = %q, %v", data, err)
	}
	// Hosts cache plugins by manifest version, so each release installs as its own.
	if data, err := os.ReadFile(filepath.Join(destination, "plugin.json")); err != nil || string(data) != `{"version":"1.2.3"}` {
		t.Fatalf("plugin manifest = %q, %v", data, err)
	}
	if got := pluginVersion("(devel)"); got != "0.0.0-devel" {
		t.Fatalf("unversioned build = %q", got)
	}
	if got := status(t, destination, source); got != "current" {
		t.Fatalf("status = %s", got)
	}
	second, err := Install(destination, source, false)
	if err != nil || second.Status != "already-installed" {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(destination)), ".conductor-install-*")); len(leftovers) != 0 {
		t.Fatalf("stages left behind: %v", leftovers)
	}
}

func TestReplacingAnotherDistributionNeedsReplaceAndKeepsABackup(t *testing.T) {
	destination := testDestination(t)
	if _, err := Install(destination, testSource(t, "old\n"), false); err != nil {
		t.Fatal(err)
	}
	incoming := testSource(t, "new\n")
	if got := status(t, destination, incoming); got != "outdated" {
		t.Fatalf("status = %s", got)
	}
	if _, err := Install(destination, incoming, false); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("install over another distribution = %v", err)
	}
	result, err := Install(destination, incoming, true)
	if err != nil || result.Status != "upgraded" || result.PreviousVersion != "v1.2.3" {
		t.Fatalf("replace = %+v, %v", result, err)
	}
	if data, _ := os.ReadFile(filepath.Join(destination, "SKILL.md")); string(data) != "new\n" {
		t.Fatalf("installed SKILL.md = %q", data)
	}
	backups, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), ".conductor", "backups", "skills", "conductor.pre-upgrade-*", "SKILL.md"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	if data, _ := os.ReadFile(backups[0]); string(data) != "old\n" {
		t.Fatalf("backed-up SKILL.md = %q", data)
	}
}

func TestAChangedOrForeignTreeIsUnknownAndNeverOverwrittenSilently(t *testing.T) {
	source := testSource(t, "skill\n")
	changed := testDestination(t)
	if _, err := Install(changed, source, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(changed, "SKILL.md"), []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	foreign := testDestination(t)
	if err := writeFile(filepath.Join(foreign, "SKILL.md"), []byte("someone else's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{changed, foreign} {
		if got := status(t, destination, source); got != "unknown" {
			t.Fatalf("status of %s = %s", destination, got)
		}
		if _, err := Install(destination, source, false); err == nil {
			t.Fatalf("install over %s succeeded without --replace", destination)
		}
		if result, err := Install(destination, source, true); err != nil || result.Status != "upgraded" {
			t.Fatalf("replace %s = %+v, %v", destination, result, err)
		}
	}
}

func TestAStagedExecutableThatDisagreesIsNeverPublished(t *testing.T) {
	destination := testDestination(t)
	if _, err := Install(destination, testSource(t, "old\n"), false); err != nil {
		t.Fatal(err)
	}
	incoming := testSource(t, "new\n")
	t.Setenv(smokeProtocolEnvironment, "2")
	if _, err := Install(destination, incoming, true); err == nil || !strings.Contains(err.Error(), "install verify") {
		t.Fatalf("mismatched executable = %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(destination, "SKILL.md")); string(data) != "old\n" {
		t.Fatalf("the working installation was displaced: SKILL.md = %q", data)
	}
}

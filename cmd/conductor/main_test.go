package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/cutover"
	installer "github.com/danlavee/Conductor/internal/install"
)

const cliHelperEnvironment = "CONDUCTOR_CLI_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(cliHelperEnvironment) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInstallArgumentFailuresDoNotInitializeRuntime(t *testing.T) {
	for name, args := range map[string][]string{
		"missing":   {"install"},
		"empty":     {"install", ""},
		"relative":  {"install", filepath.Join(".agents", "skills", "conductor")},
		"malformed": {"install", filepath.Join(t.TempDir(), ".agents", "skills", "other")},
		"extra":     {"install", "first", "second"},
	} {
		t.Run(name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "runtime-state")
			t.Setenv("CONDUCTOR_HOME", state)
			err := run(args)
			if err == nil {
				t.Fatal("invalid install invocation succeeded")
			}
			if err.Error() != "usage: conductor install <absolute-skill-directory>" {
				t.Fatalf("error = %q", err)
			}
			if _, statErr := os.Stat(state); !os.IsNotExist(statErr) {
				t.Fatalf("runtime state was initialized: %v", statErr)
			}
		})
	}
}

func TestMissingInstallFolderProcessContract(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "install")
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("error = %v, want exit code 1", err)
	}
	if len(stdout) != 0 {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(stderr, &response); err != nil {
		t.Fatalf("stderr is not JSON: %q: %v", stderr, err)
	}
	if response.Code != "INVALID" || response.Message != "usage: conductor install <absolute-skill-directory>" {
		t.Fatalf("stderr response = %+v", response)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("runtime state was initialized: %v", err)
	}
}

func TestRecordOperationProcessContract(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "join", "development")
	if err != nil || len(stderr) != 0 || len(stdout) == 0 {
		t.Fatalf("join error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "created")
	if err != nil || len(stderr) != 0 || len(stdout) == 0 {
		t.Fatalf("create error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
	var created map[string]any
	if err := json.Unmarshal(stdout, &created); err != nil || len(created) != 2 || created["index"] != float64(1) || created["text"] != "created" {
		t.Fatalf("created record = %#v, error = %v", created, err)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "edit", "messages/team", "1", "updated")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("edit error = %v, stderr = %q", err, stderr)
	}
	var edited map[string]any
	if err := json.Unmarshal(stdout, &edited); err != nil || len(edited) != 2 || edited["index"] != float64(1) || edited["text"] != "updated" {
		t.Fatalf("edited record = %#v, error = %v", edited, err)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "strike", "messages/team", "1")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("strike error = %v, stderr = %q", err, stderr)
	}
	var struck conductor.Record
	if err := json.Unmarshal(stdout, &struck); err != nil || struck.Index != 1 || struck.Text != "~~updated~~" {
		t.Fatalf("struck record = %#v, error = %v", struck, err)
	}
}

func TestPutFileBulkLoadsAsOneTransaction(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "join", "development"); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(t.TempDir(), "rules.jsonl")
	content := "\"first\"\n\n\"second\"\n\"third\"\n"
	if err := os.WriteFile(jsonlPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "--file="+jsonlPath)
	if err != nil || len(stderr) != 0 || len(stdout) == 0 {
		t.Fatalf("bulk put error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
	var publication conductor.Publication
	if err := json.Unmarshal(stdout, &publication); err != nil {
		t.Fatalf("stdout is not a publication: %q: %v", stdout, err)
	}
	if publication.Topic != "messages/team" || len(publication.Records) != 3 {
		t.Fatalf("publication = %#v", publication)
	}
	for want, record := range map[int]string{0: "first", 1: "second", 2: "third"} {
		if publication.Records[want].Index != int64(want+1) || publication.Records[want].Text != record {
			t.Fatalf("record %d = %#v, want index %d text %q", want, publication.Records[want], want+1, record)
		}
	}

	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "get", "messages/team", "--full")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("get error = %v, stderr = %q", err, stderr)
	}
	var full conductor.ReadResult
	if err := json.Unmarshal(stdout, &full); err != nil {
		t.Fatalf("stdout is not a read result: %q: %v", stdout, err)
	}
	if len(full.Records) != 3 {
		t.Fatalf("full records = %#v, want exactly the 3 bulk-loaded records (one publish signal)", full.Records)
	}
}

func TestPutFileMalformedLineAbortsWithoutPartialRecords(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "join", "development"); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(t.TempDir(), "bad.jsonl")
	content := "\"first\"\n42\n\"third\"\n"
	if err := os.WriteFile(jsonlPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "--file="+jsonlPath)
	if err == nil || len(stdout) != 0 || len(stderr) == 0 {
		t.Fatalf("malformed bulk put error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}

	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "get", "messages/team", "--full")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("get error = %v, stderr = %q", err, stderr)
	}
	var full conductor.ReadResult
	if err := json.Unmarshal(stdout, &full); err != nil {
		t.Fatalf("stdout is not a read result: %q: %v", stdout, err)
	}
	if len(full.Records) != 0 {
		t.Fatalf("full records = %#v, want none after aborted bulk load", full.Records)
	}

	// The failed bulk load must not leave a dangling transaction/lock behind:
	// a fresh begin on the same topic must succeed immediately.
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "begin", "messages/team")
	if err != nil || len(stderr) != 0 || len(stdout) == 0 {
		t.Fatalf("begin after aborted bulk load error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
	if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "abort"); err != nil {
		t.Fatal(err)
	}
}

func TestRecordOperationProcessVisibilityBoundary(t *testing.T) {
	t.Run("before-head", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "runtime-state")
		if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "join", "development"); err != nil {
			t.Fatal(err)
		}
		indexData := fmt.Sprintf("{\n  \"index\": %d\n}\n", int64(math.MaxInt64))
		if err := os.WriteFile(filepath.Join(state, "state", "index.json"), []byte(indexData), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "not visible")
		if err == nil || len(stdout) != 0 || len(stderr) == 0 {
			t.Fatalf("error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
		}
	})

	t.Run("after-head", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "runtime-state")
		if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "join", "development"); err != nil {
			t.Fatal(err)
		}
		// Registration itself already consumes global sequences 1 (the
		// collaboration/agents roster commit) and 2 (join); the put below
		// lands its own event at 3.
		if err := os.Mkdir(filepath.Join(state, "events", fmt.Sprintf("%020d.json", 3)), 0o700); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "visible")
		if err == nil || len(stdout) == 0 || len(stderr) == 0 {
			t.Fatalf("error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
		}
		var record conductor.Record
		if decodeErr := json.Unmarshal(stdout, &record); decodeErr != nil || record.Index != 1 || record.Text != "visible" {
			t.Fatalf("record = %#v, decode error = %v", record, decodeErr)
		}
	})
}

func TestProtocolMismatchProcessContract(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "join", "coordination"); err != nil {
		t.Fatal(err)
	}
	declaration := filepath.Join(state, "protocol.json")
	unsupported := conductor.CurrentProtocolVersion + 1
	declarationText := fmt.Sprintf("{\n  \"version\": %d\n}\n", unsupported)
	if err := os.WriteFile(declaration, []byte(declarationText), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "list-agents")
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 || len(stdout) != 0 {
		t.Fatalf("error = %v, stdout = %q", err, stdout)
	}
	var response struct {
		Code     string `json:"code"`
		Protocol struct {
			Supported int `json:"supported"`
			Found     int `json:"found"`
		} `json:"protocol"`
	}
	if err := json.Unmarshal(stderr, &response); err != nil {
		t.Fatalf("stderr is not JSON: %q: %v", stderr, err)
	}
	if response.Code != "PROTOCOL_MISMATCH" || response.Protocol.Supported != conductor.CurrentProtocolVersion || response.Protocol.Found != unsupported {
		t.Fatalf("response = %+v", response)
	}
	if data, readErr := os.ReadFile(declaration); readErr != nil || string(data) != declarationText {
		t.Fatalf("declaration changed: %q, %v", data, readErr)
	}
}

func TestInstallProcessSuccessAndIdempotency(t *testing.T) {
	caseRoot := t.TempDir()
	destination := filepath.Join(caseRoot, ".agents", "skills", "conductor")
	state := filepath.Join(caseRoot, "runtime-state")
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "install", destination)
	if err != nil || len(stderr) != 0 {
		t.Fatalf("first install error = %v, stderr = %q", err, stderr)
	}
	var first installer.Result
	if err := json.Unmarshal(stdout, &first); err != nil {
		t.Fatal(err)
	}
	if first.Status != "installed" || first.InstallPath != destination {
		t.Fatalf("first result = %+v", first)
	}
	if first.Protocol != conductor.CurrentProtocolVersion {
		t.Fatalf("installed protocol = %d", first.Protocol)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "install", destination)
	if err != nil || len(stderr) != 0 {
		t.Fatalf("second install error = %v, stderr = %q", err, stderr)
	}
	var second installer.Result
	if err := json.Unmarshal(stdout, &second); err != nil {
		t.Fatal(err)
	}
	if second.Status != "already-installed" || second.ManifestHash != first.ManifestHash {
		t.Fatalf("second result = %+v", second)
	}
	stdout, stderr, err = runCLIHelper(first.BinaryPath, state, t.TempDir(), "version")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("installed version error = %v, stderr = %q", err, stderr)
	}
	var version struct {
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := json.Unmarshal(stdout, &version); err != nil || version.Version == "" || version.Protocol != conductor.CurrentProtocolVersion {
		t.Fatalf("version = %q, error = %v", stdout, err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("runtime state was initialized: %v", err)
	}
}

func runCLIHelper(executable, state, directory string, args ...string) ([]byte, []byte, error) {
	command := exec.Command(executable, args...)
	command.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	command.Env = replaceEnvironment(command.Env, "CONDUCTOR_HOME", state)
	command.Dir = directory
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.EqualFold(entry[:strings.Index(entry, "=")+1], prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func TestVersionDoesNotInitializeRuntime(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("runtime state was initialized: %v", err)
	}
}

func TestVersionExposesCutoverCapability(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "version")
	if err != nil {
		t.Fatalf("version: %v: %s", err, stderr)
	}
	var version struct {
		CutoverCapability int `json:"cutover_capability"`
	}
	if err := json.Unmarshal(stdout, &version); err != nil {
		t.Fatal(err)
	}
	if version.CutoverCapability != cutover.Capability {
		t.Fatalf("cutover capability = %d", version.CutoverCapability)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("runtime state was initialized: %v", err)
	}
}

// The watch surface is host-neutral by construction: a vendor name in the
// usage string is the first symptom of delivery-to-turn leaking back into
// Conductor, which is the adapter's job and not the bus's.
func TestUsageExposesNoVendorWatchTransports(t *testing.T) {
	message := usageError().Error()
	for _, flag := range []string{"--codex-desktop", "--claude-cli", "--codex ", "--codex-cli"} {
		if strings.Contains(message, flag) {
			t.Fatalf("usage retains vendor watch transport %s: %s", flag, message)
		}
	}
	if !strings.Contains(message, "watch [--once]") {
		t.Fatalf("usage omits the host-neutral watch: %s", message)
	}
}

func TestUsageContract(t *testing.T) {
	const want = "usage: conductor install <absolute-skill-directory> | conductor verify <absolute-skill-directory> | conductor adapter claude <arm|release|identity> | conductor adapter claude install <absolute-adapter-directory> | conductor cutover <status|freeze|replace|activate|abort> ... | conductor migrate <absolute-source-root> <absolute-destination-root> | conductor version | conductor <agent> join [responsibility] | conductor <agent> leave | conductor <agent> list-agents | conductor <agent> subscribe (--topic-group=<group> | --topic=<group/topic>) | conductor <agent> list (--topic-groups | --topic-group=<group>) | conductor <agent> begin <group/topic> | conductor <agent> put <group/topic> <text> | conductor <agent> put <group/topic> --file=<path> | conductor <agent> put <text> | conductor <agent> edit <group/topic> <index> <text> | conductor <agent> edit <index> <text> | conductor <agent> strike <group/topic> <index> | conductor <agent> strike <index> | conductor <agent> commit | conductor <agent> abort | conductor <agent> get <group/topic> [index] ([--start=N] [--end=N] [--limit=N] | --delta [--limit=N] | --full) | conductor <agent> watch [--once] [--mode=summary|content] | conductor <agent> status"
	if got := usageError().Error(); got != want {
		t.Fatalf("usage = %q, want %q", got, want)
	}
}

func TestMigrateRequiresFrozenSourceAndDoesNotInitializeRuntime(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "destination")
	if err := os.WriteFile(filepath.Join(source, "protocol.json"), []byte("{\"version\":4}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)

	err := run([]string{"migrate", source, destination})
	if err == nil || err.Error() != "migration requires a frozen cutover source" {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(state); !os.IsNotExist(statErr) {
		t.Fatalf("runtime state was initialized: %v", statErr)
	}
}

func TestMigrateDispatchesOnlyAfterFreezeBarrier(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "destination")
	if err := os.WriteFile(filepath.Join(source, "protocol.json"), []byte("{\"version\":4}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cutover.Freeze(source, "cut-1", currentVersion(), nil); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)

	err := run([]string{"migrate", source, destination})
	if err == nil || err.Error() != "migrate supports v1, v2 or v3 source roots, found protocol 4" {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(state); !os.IsNotExist(statErr) {
		t.Fatalf("runtime state was initialized: %v", statErr)
	}
}

func TestInvalidAgentCommandPreservesClientInitializationOrder(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)

	err := run([]string{"a", "unknown"})
	if err == nil || err.Error() != usageError().Error() {
		t.Fatalf("error = %v, want usage error", err)
	}
	if _, statErr := os.Stat(filepath.Join(state, "protocol.json")); statErr != nil {
		t.Fatalf("client was not initialized before command validation: %v", statErr)
	}
}

func TestWatchDefaultsToContent(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "a", "join", "dev"); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "a", "watch", "--once")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("watch --once failed: %v, stderr = %q", err, stderr)
	}
	var batch conductor.BatchDelivery
	if err := json.Unmarshal(stdout, &batch); err != nil {
		t.Fatalf("stdout is not a delivery batch: %q: %v", stdout, err)
	}
	if len(batch.Deliveries) == 0 {
		t.Fatalf("batch = %#v", batch)
	}
	foundContent := false
	for _, delivery := range batch.Deliveries {
		if delivery.Mode != conductor.DeliveryContent {
			t.Fatalf("delivery = %#v", delivery)
		}
		if delivery.Delta != nil {
			foundContent = true
		}
	}
	if !foundContent {
		t.Fatalf("batch has no resolved content: %#v", batch)
	}
}

func TestOneShotWatchReleasesOwnership(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	client, err := conductor.New(state, "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("a", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := runWatch(context.Background(), client, conductor.DeliverySummary, true); err != nil {
		t.Fatal(err)
	}
	second, err := conductor.New(state, "a")
	if err != nil {
		t.Fatal(err)
	}
	release, err := second.AcquireWatchOwnership()
	if err != nil {
		t.Fatalf("one-shot watch did not release ownership: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestCutoverFreezeReplaceFireAndRearm(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	executable := os.Args[0]
	targetRelease := currentVersion()
	if _, _, err := runCLIHelper(executable, root, "", "a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLIHelper(executable, root, "", "a", "subscribe", "--topic=dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLIHelper(executable, root, "", "a", "watch", "--once", "--mode=summary"); err != nil {
		t.Fatalf("drain join: %v", err)
	}

	watch := exec.Command(executable, "a", "watch", "--mode=summary")
	watch.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	watch.Env = replaceEnvironment(watch.Env, "CONDUCTOR_HOME", root)
	var watchOut, watchErr bytes.Buffer
	watch.Stdout = &watchOut
	watch.Stderr = &watchErr
	if err := watch.Start(); err != nil {
		t.Fatal(err)
	}
	controlDir, err := cutover.Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(controlDir, "watch", "a.owner.json")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(owner); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = watch.Process.Kill()
			t.Fatal("watcher did not publish cutover capability")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, stderr, err := runCLIHelper(executable, root, "", "cutover", "freeze", root, "--id=cut-1", "--release="+targetRelease); err != nil {
		_ = watch.Process.Kill()
		t.Fatalf("freeze: %v: %s", err, stderr)
	}
	if _, stderr, err := runCLIHelper(executable, root, "", "a", "put", "dev/tasks", "blocked"); err == nil {
		_ = watch.Process.Kill()
		t.Fatal("write succeeded while frozen")
	} else if !bytes.Contains(stderr, []byte("frozen")) {
		t.Fatalf("blocked write error = %s", stderr)
	}
	if _, stderr, err := runCLIHelper(executable, root, "", "cutover", "replace", root, "--id=cut-1"); err != nil {
		_ = watch.Process.Kill()
		t.Fatalf("replace: %v: %s", err, stderr)
	}
	waited := make(chan error, 1)
	go func() { waited <- watch.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("watch: %v: %s", err, watchErr.String())
		}
	case <-time.After(3 * time.Second):
		_ = watch.Process.Kill()
		t.Fatal("watch did not fire replacement activation")
	}
	var activation conductor.ReplacementActivation
	if err := json.Unmarshal(watchOut.Bytes(), &activation); err != nil {
		t.Fatalf("activation JSON: %v: %s", err, watchOut.String())
	}
	if activation.Type != "conductor-replaced" || activation.CutoverID != "cut-1" || activation.Release != targetRelease {
		t.Fatalf("activation = %#v", activation)
	}
	if _, stderr, err := runCLIHelper(executable, root, "", "cutover", "activate", root, "--id=cut-1"); err != nil {
		t.Fatalf("activate: %v: %s", err, stderr)
	}
	if _, _, err := runCLIHelper(executable, root, "", "a", "put", "dev/tasks", "fresh"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(executable, root, "", "a", "watch", "--once", "--mode=summary")
	if err != nil {
		t.Fatalf("rearm: %v: %s", err, stderr)
	}
	var batch conductor.BatchDelivery
	if err := json.Unmarshal(stdout, &batch); err != nil || len(batch.Deliveries) == 0 {
		t.Fatalf("rearmed delivery = %#v, %v: %s", batch, err, stdout)
	}
}

func TestRemovedWatchModesReturnUsage(t *testing.T) {
	t.Setenv("CONDUCTOR_HOME", filepath.Join(t.TempDir(), "runtime-state"))
	for _, args := range [][]string{
		{"a", "watch", "--wait"},
		{"a", "watch", "--since", "1"},
		{"a", "watch", "--codex"},
		{"a", "watch", "--codex-cli"},
		{"a", "watch", "--codex-desktop"},
		{"a", "watch", "--claude-cli"},
		{"a", "watch", "--once", "--once"},
		{"a", "status", "extra"},
	} {
		if err := run(args); err == nil || err.Error() != usageError().Error() {
			t.Fatalf("%v: error = %v, want usage error", args, err)
		}
	}
}

// A continuous watch must hold ownership across the quiet gap between
// deliveries. If it released after draining a batch, a second watcher could
// arm for the same identity and both would deliver.
func TestContinuousWatchRetainsOwnershipAfterDelivering(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	watch, deliveries := startWatch(t, root)
	defer stopWatch(t, watch)

	var batch conductor.BatchDelivery
	if err := deliveries.Decode(&batch); err != nil {
		t.Fatalf("continuous watch delivered nothing: %v", err)
	}
	if len(batch.Deliveries) == 0 {
		t.Fatalf("batch = %#v", batch)
	}

	client, err := conductor.New(root, "a")
	if err != nil {
		t.Fatal(err)
	}
	if release, err := client.AcquireWatchOwnership(); err == nil {
		_ = release()
		t.Fatal("a second watcher armed while a live stream owned the identity")
	}
}

// status is the answer an adapter needs before deciding to re-arm, so it must
// distinguish "no stream" from "a stream that died without releasing". A
// killed watcher leaves its diagnostic sidecar behind; reporting that as
// wakeable is exactly the silent unwakeability this design exists to remove.
func TestStatusReportsRegistrationAndLiveWakeability(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")

	before := readStatus(t, root)
	if before.Agent != "a" || before.Registered || before.Wakeable {
		t.Fatalf("status before join = %#v", before)
	}
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	joined := readStatus(t, root)
	if !joined.Registered || joined.Wakeable {
		t.Fatalf("status after join = %#v", joined)
	}

	// Registered before waiting on ownership, not after: every step between
	// here and the deliberate stop below can t.Fatal, and a watcher left alive
	// by one of them still holds a handle inside the directory the test's own
	// cleanup then tries to remove. Stopping twice is harmless -- stopWatch
	// discards both the kill and the wait error -- so the insurance costs
	// nothing on the path where the test succeeds.
	watch, _ := startWatch(t, root)
	defer stopWatch(t, watch)
	waitForOwnership(t, root)
	watching := readStatus(t, root)
	if !watching.Registered || !watching.Wakeable || watching.PID != watch.Process.Pid {
		t.Fatalf("status while watching = %#v, watcher pid = %d", watching, watch.Process.Pid)
	}

	// The stop is the thing under test here, not just teardown: a dead watcher
	// must read as unwakeable while its sidecar is still on disk.
	stopWatch(t, watch)
	controlDir, err := cutover.Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(controlDir, "watch", "a.owner.json")); err != nil {
		t.Fatalf("killed watcher removed its sidecar, so the stale case is untested: %v", err)
	}
	dead := readStatus(t, root)
	if !dead.Registered || dead.Wakeable {
		t.Fatalf("status after the watcher died = %#v", dead)
	}
}

// The roster answers who would respond, not who once signed up. Both halves
// matter: an agent holding a live stream has to read as wakeable, and one that
// registered and then lost its stream has to read as not -- reporting the
// second as available is the silent unwakeability this design exists to remove.
func TestListAgentsReportsWhoWouldRespond(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	for agent, responsibility := range map[string]string{"a": "dev", "b": "review"} {
		if _, stderr, err := runCLIHelper(os.Args[0], root, "", agent, "join", responsibility); err != nil {
			t.Fatalf("join %s: %v: %s", agent, err, stderr)
		}
	}
	// Registered before waiting on ownership, not after: waitForOwnership can
	// t.Fatal, and a defer registered later would never run -- leaking a
	// watcher that still holds a handle inside the directory the test's own
	// cleanup then tries to remove.
	watch, _ := startWatch(t, root)
	defer stopWatch(t, watch)
	waitForOwnership(t, root)

	stdout, stderr, err := runCLIHelper(os.Args[0], root, "", "b", "list-agents")
	if err != nil {
		t.Fatalf("list-agents: %v: %s", err, stderr)
	}
	var roster []conductor.RosterEntry
	if err := json.Unmarshal(stdout, &roster); err != nil {
		t.Fatalf("roster is not decodable: %q: %v", stdout, err)
	}
	byName := map[string]conductor.RosterEntry{}
	for _, entry := range roster {
		byName[entry.Name] = entry
	}
	if len(byName) != 2 {
		t.Fatalf("roster = %#v", roster)
	}
	// The roster still carries the registration it always did, alongside the
	// observation; wakeability is reported beside that record, not stored in it.
	if watching := byName["a"]; !watching.Wakeable || watching.PID != watch.Process.Pid || watching.Responsibility != "dev" {
		t.Fatalf("watching agent = %#v, watcher pid = %d", watching, watch.Process.Pid)
	}
	if idle := byName["b"]; idle.Wakeable || idle.PID != 0 {
		t.Fatalf("agent with no stream = %#v", idle)
	}
}

// A wakeability query is asked from outside the bus, and the two ways it could
// fail to be are both silent. Initializing a protocol root in order to report
// on it creates the thing being asked about; refusing to answer mid-cutover
// withholds the answer exactly when an operator most wants it, since a frozen
// bus is when "would anyone respond" stops being obvious.
func TestStatusNeitherInitializesNorEntersTheBus(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")

	before := readStatus(t, root)
	if before.Registered || before.Wakeable {
		t.Fatalf("status on an uninitialized root = %#v", before)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("status initialized the protocol root: %v", err)
	}

	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatalf("join: %v: %s", err, stderr)
	}
	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "cutover", "freeze", root, "--id=cut-1", "--release="+currentVersion()); err != nil {
		t.Fatalf("freeze: %v: %s", err, stderr)
	}
	// Confirm the freeze is actually in force, so the assertion below is about
	// status tolerating it rather than about nothing being frozen.
	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "put", "dev/tasks", "blocked"); err == nil {
		t.Fatal("write succeeded while frozen")
	} else if !bytes.Contains(stderr, []byte("frozen")) {
		t.Fatalf("blocked write error = %s", stderr)
	}
	frozen := readStatus(t, root)
	if !frozen.Registered || frozen.Wakeable {
		t.Fatalf("status while frozen = %#v", frozen)
	}
}

// startWatch runs a continuous watch as a real process, and hands back a
// decoder over its stdout so a test can wait on an actual delivery rather than
// poll a buffer the child is concurrently writing.
func startWatch(t *testing.T, root string) (*exec.Cmd, *json.Decoder) {
	t.Helper()
	watch := exec.Command(os.Args[0], "a", "watch")
	watch.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	watch.Env = replaceEnvironment(watch.Env, "CONDUCTOR_HOME", root)
	stdout, err := watch.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := watch.Start(); err != nil {
		t.Fatal(err)
	}
	return watch, json.NewDecoder(stdout)
}

func stopWatch(t *testing.T, watch *exec.Cmd) {
	t.Helper()
	_ = watch.Process.Kill()
	_ = watch.Wait()
}

func waitForOwnership(t *testing.T, root string) {
	t.Helper()
	controlDir, err := cutover.Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(controlDir, "watch", "a.owner.json")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(owner); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("watcher never took ownership")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func readStatus(t *testing.T, root string) conductor.WatchStatus {
	t.Helper()
	stdout, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "status")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("status failed: %v, stderr = %q", err, stderr)
	}
	var status conductor.WatchStatus
	if err := json.Unmarshal(stdout, &status); err != nil {
		t.Fatalf("status output is not JSON: %q: %v", stdout, err)
	}
	return status
}

func TestParseGetRangeDefaultsAndGuards(t *testing.T) {
	request, err := parseGet([]string{"messages/team", "7", "--start=0", "--end=12", "--limit=5"})
	if err != nil {
		t.Fatal(err)
	}
	if request.Topic != "messages/team" || request.RecordIndex != 7 || request.Mode != conductor.ReadRange || request.Start != 0 || request.End != 12 || request.Limit != 5 {
		t.Fatalf("request = %+v", request)
	}
	defaults, err := parseGet([]string{"messages/team"})
	if err != nil || defaults.Start != 0 || defaults.End != 0 || defaults.Limit != 0 || defaults.Mode != conductor.ReadRange {
		t.Fatalf("defaults = %+v, error = %v", defaults, err)
	}
}

func TestParseGetDeltaAndFull(t *testing.T) {
	delta, err := parseGet([]string{"messages/team", "7", "--delta", "--limit=5"})
	if err != nil || delta.Mode != conductor.ReadDelta || delta.RecordIndex != 7 || delta.Limit != 5 {
		t.Fatalf("delta = %+v, error = %v", delta, err)
	}
	full, err := parseGet([]string{"messages/team", "--full"})
	if err != nil || full.Mode != conductor.ReadFull {
		t.Fatalf("full = %+v, error = %v", full, err)
	}
	for _, args := range [][]string{
		{"messages/team", "--delta", "--start=1"},
		{"messages/team", "--full", "--limit=1"},
		{"messages/team", "--delta", "--full"},
	} {
		if _, err := parseGet(args); err == nil {
			t.Fatalf("parseGet(%v) succeeded, want error", args)
		}
	}
}

func TestGetDeltaAcknowledgesSuccessfulDelivery(t *testing.T) {
	home := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", home)
	t.Setenv("CONDUCTOR_AGENT", "a")
	if err := run([]string{"a", "join", "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"a", "subscribe", "--topic=messages/team"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"a", "put", "messages/team", "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"a", "get", "messages/team", "--delta"}); err != nil {
		t.Fatal(err)
	}
	client, err := conductor.New(home, "a")
	if err != nil {
		t.Fatal(err)
	}
	delta, err := client.Get(conductor.ReadRequest{Topic: "messages/team", Mode: conductor.ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Publications) != 0 {
		t.Fatalf("CLI delta was not acknowledged: %#v", delta)
	}
}

func TestParseRecordIndex(t *testing.T) {
	if index, err := parseRecordIndex("7"); err != nil || index != 7 {
		t.Fatalf("index = %d, error = %v", index, err)
	}
	for _, value := range []string{"", "0", "-1", "text"} {
		if _, err := parseRecordIndex(value); err == nil {
			t.Fatalf("invalid index %q succeeded", value)
		}
	}
}

func TestParseRedact(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantTopic string
		wantStart int64
		wantEnd   int64
		wantError string
	}{
		{name: "single record", args: []string{"dev/tasks", "7"}, wantTopic: "dev/tasks", wantStart: 7, wantEnd: 7},
		{name: "range", args: []string{"dev/tasks", "--end=9", "--start=2"}, wantTopic: "dev/tasks", wantStart: 2, wantEnd: 9},
		{name: "missing arguments", args: []string{"dev/tasks"}, wantError: usageError().Error()},
		{name: "too many arguments", args: []string{"dev/tasks", "1", "2", "3"}, wantError: usageError().Error()},
		{name: "invalid record", args: []string{"dev/tasks", "zero"}, wantError: "record index must be a positive integer"},
		{name: "invalid start", args: []string{"dev/tasks", "--start=zero", "--end=2"}, wantError: `invalid --start: strconv.ParseInt: parsing "zero": invalid syntax`},
		{name: "invalid end", args: []string{"dev/tasks", "--start=1", "--end=zero"}, wantError: `invalid --end: strconv.ParseInt: parsing "zero": invalid syntax`},
		{name: "unknown option", args: []string{"dev/tasks", "--start=1", "--last=2"}, wantError: usageError().Error()},
		{name: "missing bound", args: []string{"dev/tasks", "--start=1", "--start=2"}, wantError: "invalid redact range"},
		{name: "zero bound", args: []string{"dev/tasks", "--start=0", "--end=2"}, wantError: "invalid redact range"},
		{name: "reversed range", args: []string{"dev/tasks", "--start=3", "--end=2"}, wantError: "invalid redact range"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			topic, start, end, err := parseRedact(test.args)
			if test.wantError != "" {
				if err == nil || err.Error() != test.wantError {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if topic != test.wantTopic || start != test.wantStart || end != test.wantEnd {
				t.Fatalf("parseRedact() = %q, %d, %d; want %q, %d, %d", topic, start, end, test.wantTopic, test.wantStart, test.wantEnd)
			}
		})
	}
}

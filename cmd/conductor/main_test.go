package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	installer "github.com/danlavee/Conductor/internal/install"
	conductor "github.com/danlavee/Conductor/internal/state"
)

const cliHelperEnvironment = "CONDUCTOR_CLI_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(cliHelperEnvironment) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const installUsage = "usage: conductor install <absolute-skill-directory> [--replace]"

func TestInstallArgumentFailuresDoNotInitializeRuntime(t *testing.T) {
	for name, invocation := range map[string]struct {
		args    []string
		message string
	}{
		"missing":     {args: []string{"install"}, message: installUsage},
		"empty":       {args: []string{"install", ""}, message: installUsage},
		"relative":    {args: []string{"install", filepath.Join(".agents", "skills", "conductor")}, message: installUsage},
		"malformed":   {args: []string{"install", filepath.Join(t.TempDir(), ".agents", "skills", "other")}, message: installUsage},
		"extra":       {args: []string{"install", "first", "second"}, message: "unrecognized option second"},
		"replaceBare": {args: []string{"install", "first", "--replace"}, message: installUsage},
	} {
		t.Run(name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "runtime-state")
			t.Setenv("CONDUCTOR_HOME", state)
			err := run(invocation.args)
			if err == nil {
				t.Fatal("invalid install invocation succeeded")
			}
			if err.Error() != invocation.message {
				t.Fatalf("error = %q, want %q", err, invocation.message)
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
	if response.Code != "INVALID" || response.Message != installUsage {
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
	// Every record verb answers with a dispatch: the publication it made and
	// who it reached. `recipients` is always present, and empty here because
	// the one agent on this bus is subscribed to nothing -- which is the fact
	// a record-only answer left an agent no way to learn.
	var created conductor.Dispatch
	if err := json.Unmarshal(stdout, &created); err != nil || created.Record().Index != 1 || created.Record().Text != "created" {
		t.Fatalf("created dispatch = %#v, error = %v", created, err)
	}
	if created.Recipients == nil || len(created.Recipients) != 0 {
		t.Fatalf("recipients = %#v; want an empty list, never absent or null", created.Recipients)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "edit", "messages/team", "1", "updated")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("edit error = %v, stderr = %q", err, stderr)
	}
	var edited conductor.Dispatch
	if err := json.Unmarshal(stdout, &edited); err != nil || edited.Record().Index != 1 || edited.Record().Text != "updated" {
		t.Fatalf("edited dispatch = %#v, error = %v", edited, err)
	}
}

// A body given as a file is published verbatim, so nothing an agent writes
// has to survive shell quoting to reach a record intact.
func TestFileOperandCarriesTextVerbatim(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "join", "development"); err != nil {
		t.Fatal(err)
	}
	body := "line one\n  \"quoted\" $HOME `backtick`\nline three"
	bodyPath := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyPath, []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "--file="+bodyPath)
	if err != nil || len(stderr) != 0 {
		t.Fatalf("put --file error = %v, stderr = %q", err, stderr)
	}
	var dispatch conductor.Dispatch
	if err := json.Unmarshal(stdout, &dispatch); err != nil {
		t.Fatalf("stdout is not a dispatch: %q: %v", stdout, err)
	}
	if dispatch.Record().Index != 1 || dispatch.Record().Text != body {
		t.Fatalf("dispatch = %#v, want index 1 text %q", dispatch, body)
	}

	editPath := filepath.Join(t.TempDir(), "edit.txt")
	if err := os.WriteFile(editPath, []byte("replaced\nstill multi-line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "edit", "messages/team", "1", "--file="+editPath)
	if err != nil || len(stderr) != 0 {
		t.Fatalf("edit --file error = %v, stderr = %q", err, stderr)
	}
	if err := json.Unmarshal(stdout, &dispatch); err != nil {
		t.Fatalf("stdout is not a dispatch: %q: %v", stdout, err)
	}
	if dispatch.Record().Text != "replaced\nstill multi-line" {
		t.Fatalf("edited dispatch = %#v", dispatch)
	}

	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, []byte("\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "--file="+empty); err == nil {
		t.Fatal("a file with no text published a record")
	}
}

func TestPutJSONLBulkLoadsAsOnePublication(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "join", "development"); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(t.TempDir(), "rules.jsonl")
	content := "\"first\"\n\n\"second\"\n\"third\"\n"
	if err := os.WriteFile(jsonlPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "--jsonl="+jsonlPath)
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

func TestPutJSONLMalformedLineAbortsWithoutPartialRecords(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "join", "development"); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(t.TempDir(), "bad.jsonl")
	content := "\"first\"\n42\n\"third\"\n"
	if err := os.WriteFile(jsonlPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "--jsonl="+jsonlPath)
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
}

// A failed put prints no dispatch, so stdout never claims a record that did
// not land.
func TestFailedPutPrintsNoDispatch(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "join", "development"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(state, "topics", "messages", "team", "history.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "not visible")
	if err == nil || len(stdout) != 0 || len(stderr) == 0 {
		t.Fatalf("error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
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
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "list", "--topic-groups")
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
	if second.Status != "already-installed" || second.BinaryPath != first.BinaryPath {
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
	if !strings.Contains(message, "<agent> watch |") {
		t.Fatalf("usage omits the host-neutral watch: %s", message)
	}
}

func TestUsageContract(t *testing.T) {
	const want = "usage: conductor <agent> join [responsibility] | conductor <agent> put <group/topic> (<text> | --file=<path> | --jsonl=<path>) | conductor <agent> edit <group/topic> <index> (<text> | --file=<path>) | conductor <agent> get <group/topic> [index] ([--start=N] [--end=N] [--limit=N] | (--delta | --full) [--limit=N]) | conductor <agent> subscribe (--topic-group=<group> | --topic=<group/topic>) | conductor <agent> list (--topic-groups | --topic-group=<group>) | conductor <agent> watch | conductor <agent> status | conductor <agent> leave | conductor install <absolute-skill-directory> [--replace] | conductor verify <absolute-skill-directory> | conductor version | conductor adapter <host> <command>"
	if got := usageError().Error(); got != want {
		t.Fatalf("usage = %q, want %q", got, want)
	}
}

// Hook verbs are for hooks. An agent that mistypes a command reads the usage,
// so a hook verb there is a command it may try; each host answers for its own.
func TestAdapterUsageStaysWithItsHost(t *testing.T) {
	for _, args := range [][]string{{"adapter"}, {"adapter", "nosuchhost"}} {
		if err := run(args); err == nil || err.Error() != adapterHostsUsageError().Error() {
			t.Fatalf("%v: error = %v, want the host list", args, err)
		}
	}
	for host, want := range map[string]error{"claude": adapterUsageError(), "grok": grokUsageError(), "agy": agyAdapterUsageError()} {
		if err := run([]string{"adapter", host, "nosuchverb"}); err == nil || err.Error() != want.Error() {
			t.Fatalf("%s: error = %v, want %v", host, err, want)
		}
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

func TestWatchResolvesContentBeforeWaking(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "a", "join", "dev"); err != nil {
		t.Fatalf("join failed: %v", err)
	}
	// An agent is not woken by its own join, so a peer's roster line is what
	// this one-shot watch delivers.
	if _, _, err := runCLIHelper(os.Args[0], state, "", "peer", "join", "review"); err != nil {
		t.Fatalf("peer join failed: %v", err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "a", "watch")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("watch failed: %v, stderr = %q", err, stderr)
	}
	var batch conductor.Batch
	if err := json.Unmarshal(stdout, &batch); err != nil {
		t.Fatalf("stdout is not a delivery batch: %q: %v", stdout, err)
	}
	if len(batch.Deliveries) != 1 || batch.Deliveries[0].Topic != "collaboration/agents" || len(batch.Deliveries[0].Publications) != 1 || batch.Deliveries[0].Publications[0].Records[0].Text != "peer: review" {
		t.Fatalf("batch = %#v", batch)
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
	// A peer's registration is what the one-shot watch below delivers on: an
	// agent is not woken by its own join.
	peer, err := conductor.New(state, "peer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Join("peer", "review"); err != nil {
		t.Fatal(err)
	}
	if err := runWatch(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	second, err := conductor.New(state, "a")
	if err != nil {
		t.Fatal(err)
	}
	release, err := second.AcquireWatchOwnership(conductor.HolderSelf)
	if err != nil {
		t.Fatalf("one-shot watch did not release ownership: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
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
		// --mode went the same way as the vendor flags, and for the same
		// reason: it was adapter configuration on a surface no adapter reaches.
		// An adapter holds the stream through the client and chooses there.
		{"a", "watch", "--mode=summary"},
		{"a", "watch", "--mode=content"},
		// Watch is one delivery and exit, always; there is no mode to pick.
		{"a", "watch", "--once"},
		{"a", "status", "extra"},
	} {
		if err := run(args); err == nil || err.Error() != usageError().Error() {
			t.Fatalf("%v: error = %v, want usage error", args, err)
		}
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

	watch, _ := startWatch(t, root)
	watching := readStatus(t, root)
	if !watching.Registered || !watching.Wakeable || watching.Holder != conductor.HolderSelf {
		t.Fatalf("status while watching = %#v", watching)
	}

	// The stop is the thing under test here, not just teardown: a dead watcher
	// must read as unwakeable while its sidecar is still on disk.
	stopWatch(t, watch)
	if _, err := os.Stat(filepath.Join(root, "state", "watch", "a.owner.json")); err != nil {
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
// The roster is handed out by join and nowhere else, so join is where the
// "who would respond" answer has to hold: registration read from disk, and
// wakeability observed live beside it rather than stored.
func TestJoinSnapshotReportsWhoWouldRespond(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	// b registers first, so a joins with nothing pending and its one-shot
	// watch is still holding when the snapshot is taken.
	for _, join := range [][2]string{{"b", "review"}, {"a", "dev"}} {
		if _, stderr, err := runCLIHelper(os.Args[0], root, "", join[0], "join", join[1]); err != nil {
			t.Fatalf("join %s: %v: %s", join[0], err, stderr)
		}
	}
	startWatch(t, root)

	stdout, stderr, err := runCLIHelper(os.Args[0], root, "", "b", "join")
	if err != nil {
		t.Fatalf("join: %v: %s", err, stderr)
	}
	var snapshot struct {
		Agents []conductor.RosterEntry `json:"agents"`
	}
	if err := json.Unmarshal(stdout, &snapshot); err != nil {
		t.Fatalf("snapshot is not decodable: %q: %v", stdout, err)
	}
	byName := map[string]conductor.RosterEntry{}
	for _, entry := range snapshot.Agents {
		byName[entry.Name] = entry
	}
	if len(byName) != 2 {
		t.Fatalf("roster = %#v", snapshot.Agents)
	}
	// The roster still carries the registration it always did, alongside the
	// observation; wakeability is reported beside that record, not stored in it.
	if watching := byName["a"]; !watching.Wakeable || watching.Holder != conductor.HolderSelf || watching.Responsibility != "dev" {
		t.Fatalf("watching agent = %#v", watching)
	}
	if idle := byName["b"]; idle.Wakeable || idle.Holder != "" {
		t.Fatalf("agent with no stream = %#v", idle)
	}
}

// A wakeability query is asked from outside the bus: initializing a protocol
// root in order to report on it would create the thing being asked about.
func TestStatusDoesNotInitializeTheBus(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")

	before := readStatus(t, root)
	if before.Registered || before.Wakeable {
		t.Fatalf("status on an uninitialized root = %#v", before)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("status initialized the protocol root: %v", err)
	}
}

// startWatch runs agent a's watch as a real process, returns once it holds the
// stream, and hands back a decoder over its stdout so a test can wait on an
// actual delivery rather than poll a buffer the child is concurrently writing.
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
	// Cleanup before the wait, which can t.Fatal: a leaked watcher holds a
	// handle inside the directory the test's own cleanup removes. Stopping
	// twice is harmless, so a test may also stop it deliberately.
	t.Cleanup(func() { stopWatch(t, watch) })
	waitForOwnership(t, root)
	return watch, json.NewDecoder(stdout)
}

func stopWatch(t *testing.T, watch *exec.Cmd) {
	t.Helper()
	_ = watch.Process.Kill()
	_ = watch.Wait()
}

func waitForOwnership(t *testing.T, root string) {
	t.Helper()
	owner := filepath.Join(root, "state", "watch", "a.owner.json")
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
	full, err := parseGet([]string{"messages/team", "--full", "--limit=50"})
	if err != nil || full.Mode != conductor.ReadFull || full.Limit != 50 {
		t.Fatalf("full = %+v, error = %v", full, err)
	}
	for _, args := range [][]string{
		{"messages/team", "--delta", "--start=1"},
		{"messages/team", "--full", "--end=1"},
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

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/claude"
	conductor "github.com/danlavee/Conductor/internal/state"
)

// TestStatusReportsWhatServesThisHost covers the third host state. Wakeability
// answers whether a stream is held; it cannot say whether an adapter is
// standing by to hold one, and an agent that reads "not wakeable" as "no
// adapter here" arms a watcher the adapter collides with when it takes the
// identity. All three states have to be distinguishable in the answer.
func TestStatusReportsWhatServesThisHost(t *testing.T) {
	root := t.TempDir()
	working := workInTemporaryDirectory(t)

	t.Setenv(claude.SessionEnvironment, "")
	report, err := hostAdapter(root)
	if err != nil || report != nil {
		t.Fatalf("adapter = %+v, %v; want none where no adapter serves the host", report, err)
	}

	// A session identifier and no adapter: the ordinary state of this host,
	// since the skill and the plugin are separate installs. It has to read as
	// no adapter, or the skill routes an agent into binding a name nothing
	// reads.
	t.Setenv(claude.SessionEnvironment, "session-1")
	report, err = hostAdapter(root)
	if err != nil || report != nil {
		t.Fatalf("adapter = %+v, %v; want none where only the session says so", report, err)
	}

	// An adapter is here and holds no identity for this session. This is the
	// state that reads identically to "no adapter" in wakeability alone.
	if err := claude.RecordSession(root, "session-1"); err != nil {
		t.Fatal(err)
	}
	report, err = hostAdapter(root)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || report.Name != "claude" || report.Agent != "" || report.Source != string(claude.NoBinding) {
		t.Fatalf("adapter = %+v; want an unbound claude adapter", report)
	}

	if err := os.WriteFile(filepath.Join(working, claude.IdentityFile), []byte("planner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = hostAdapter(root)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || report.Agent != "planner" || report.Source != string(claude.FromProject) {
		t.Fatalf("adapter = %+v; want the project's identity", report)
	}

	// A session binding outranks the project, and the reported source has to
	// say which one answered -- they are undone differently.
	if _, err := claude.BindSession(root, "session-1", "reviewer"); err != nil {
		t.Fatal(err)
	}
	report, err = hostAdapter(root)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || report.Agent != "reviewer" || report.Source != string(claude.FromSession) {
		t.Fatalf("adapter = %+v; want the session's identity", report)
	}
}

// TestArmingAnswersBeforeItWaits covers the act an agent used to conclude from
// with nothing in hand. Starting the watcher backgrounds a process and hands
// back a host pid, which says a process exists -- not that the identity is
// reachable, and not that anything reaches it. The answer has to arrive at arm
// time, before the wait, because that is when the agent decides the act is done
// and ends its turn; a fact that only appears with the first delivery arrives
// after every decision it was needed for.
func TestArmingAnswersBeforeItWaits(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "nomad", "join", "parser"); err != nil {
		t.Fatal(err)
	}
	// A peer's registration is what lets the one-shot watch below terminate;
	// nomad's own join reaches its peers, not its own inbox.
	if _, _, err := runCLIHelper(os.Args[0], root, "", "peer", "join", "review"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], root, "", "nomad", "watch", "--once")
	if err != nil {
		t.Fatalf("watch: %v: %s", err, stderr)
	}
	armed := readArmAcknowledgement(t, json.NewDecoder(bytes.NewReader(stdout)))
	if !armed.Registered || armed.PID == 0 {
		t.Fatalf("arm acknowledgement = %#v; want the registration and the holding process", armed)
	}
	// The other half of the same answer, and the one an agent cannot get from
	// the host: it is held, and nothing beyond the two broadcast topics
	// reaches it. Reachable-looking and unreachable is the state this reports.
	if len(armed.Subscriptions.TopicGroups) != 0 || len(armed.Subscriptions.Topics) != 0 {
		t.Fatalf("subscriptions = %+v; want the empty set an agent that subscribed to nothing has", armed.Subscriptions)
	}
}

// TestArmingAnswersOnAStreamThatNeverDelivers is the fault shape itself. An
// agent arms, nothing is pending, and it ends the turn -- which is where the
// claim of reachability used to be invented, because the alternative was a
// silent process. The answer has to arrive with no delivery behind it and while
// the watcher is still blocked, or it is not available when it is needed.
func TestArmingAnswersOnAStreamThatNeverDelivers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	// Nothing to consume first: what an agent's join publishes goes to its
	// peers, never to its own inbox, so this stream starts empty.
	watch, deliveries := startWatch(t, root)
	defer stopWatch(t, watch)

	// startWatch already read the acknowledgement. Nothing may follow it: a
	// second document here would mean the answer rode in on a delivery.
	quiet := make(chan error, 1)
	go func() {
		var next json.RawMessage
		quiet <- deliveries.Decode(&next)
	}()
	select {
	case err := <-quiet:
		t.Fatalf("something followed the arm acknowledgement on an empty stream: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestPublishingDoesNotEndTheStreamThatArmedIt is the field failure this test
// file's neighbours could not catch. An agent follows the order the skill sets
// -- arm, then report what it has taken -- and its own put comes back to it as
// a delivery, ending the one-shot stream it just armed. The turn then closes
// with a claim of reachability that was true when written and false when read.
func TestPublishingDoesNotEndTheStreamThatArmedIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "subscribe", "--topic=dev/log"); err != nil {
		t.Fatal(err)
	}
	watch, deliveries := startWatch(t, root)
	defer stopWatch(t, watch)

	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "put", "dev/log", "picked this up"); err != nil {
		t.Fatalf("put: %v: %s", err, stderr)
	}
	delivered := make(chan error, 1)
	go func() {
		var next json.RawMessage
		delivered <- deliveries.Decode(&next)
	}()
	select {
	case err := <-delivered:
		t.Fatalf("the stream took the agent's own publication as a delivery: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	// Still held, so the record the agent is waiting for can still reach it.
	stdout, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "status")
	if err != nil {
		t.Fatalf("status: %v: %s", err, stderr)
	}
	var reported conductor.WatchStatus
	if err := json.Unmarshal(stdout, &reported); err != nil {
		t.Fatalf("status JSON: %v: %s", err, stdout)
	}
	if !reported.Wakeable {
		t.Fatalf("publishing left the agent unreachable: %+v", reported)
	}
}

// TestAFailedWaitStillCarriesItsArmAnswer pins what a reader of the stream can
// conclude from counting documents. The arm answer is written before the wait,
// so a watch whose wait ends in failure exits having written one document and
// not two -- and a reader holding "two documents" as the contract would take
// that for a delivery still on its way. Leaving the roster while blocked is the
// cheapest way to fail a wait; every other failure exits in the same shape.
func TestAFailedWaitStillCarriesItsArmAnswer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "nomad", "join", "parser"); err != nil {
		t.Fatal(err)
	}
	// Nothing to drain: what an agent's join publishes goes to its peers, never
	// to its own inbox, so the watcher below blocks instead of resolving a
	// backlog and exiting successfully.
	watch := exec.Command(os.Args[0], "nomad", "watch", "--once")
	watch.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	watch.Env = replaceEnvironment(watch.Env, "CONDUCTOR_HOME", root)
	stdout, err := watch.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	watch.Stderr = &stderr
	if err := watch.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopWatch(t, watch)
	deliveries := json.NewDecoder(stdout)
	readArmAcknowledgement(t, deliveries)

	if _, _, err := runCLIHelper(os.Args[0], root, "", "nomad", "leave"); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		var next json.RawMessage
		ended <- deliveries.Decode(&next)
	}()
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("watch wrote a second document after its identity left the roster")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch kept waiting for an identity that is no longer on the roster")
	}
	if err := watch.Wait(); err == nil {
		t.Fatal("watch exited 0 after the identity it was waiting for left the roster")
	}
	if !strings.Contains(stderr.String(), "NOT_FOUND") {
		t.Fatalf("watch stderr = %s; want the failure an absent identity earns", stderr.String())
	}
}

// TestArmingRefusesRatherThanArmingBlind covers what arming does when it cannot
// read the host. Composing the adapter can fail -- an unusable session
// identifier is enough -- and the tempting shape is to shrug it off and arm
// anyway, which is how a watcher collides with an adapter that later claims the
// identity. The refusal has to be the whole command: nothing on stdout, and the
// identity left exactly as unheld as it was.
func TestArmingRefusesRatherThanArmingBlind(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "nomad", "join", "parser"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(claude.SessionEnvironment, "not/a/session")
	stdout, stderr, err := runCLIHelper(os.Args[0], root, t.TempDir(), "nomad", "watch", "--once")
	if err == nil || !strings.Contains(string(stderr), "INVALID") {
		t.Fatalf("watch = %s / %s, %v; want the refusal an unusable session earns", stdout, stderr, err)
	}
	if len(stdout) != 0 {
		t.Fatalf("watch wrote %s; a refused arm answers nothing", stdout)
	}

	// The refusal has to leave the stream untaken. A watcher that acquired
	// ownership and then failed would report here as wakeable with a pid whose
	// process is already gone.
	t.Setenv(claude.SessionEnvironment, "")
	stdout, stderr, err = runCLIHelper(os.Args[0], root, t.TempDir(), "nomad", "status")
	if err != nil {
		t.Fatalf("status: %v: %s", err, stderr)
	}
	var report statusReport
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatal(err)
	}
	if report.Wakeable || report.Holder != nil {
		t.Fatalf("status = %s; want an identity nothing holds after the refusal", stdout)
	}
}

// TestStatusCommandCarriesTheAdapterToStdout is the wiring test. Composing the
// host's binding is done at this layer precisely so internal/state does not
// import an adapter, which means nothing below this layer can prove the
// composition is still attached -- and a status answer that silently dropped it
// would read exactly like a host with no adapter, the confusion the field
// exists to remove.
func TestStatusCommandCarriesTheAdapterToStdout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv(claude.SessionEnvironment, "session-1")
	if err := claude.RecordSession(root, "session-1"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], root, t.TempDir(), "nomad", "status")
	if err != nil {
		t.Fatalf("status: %v: %s", err, stderr)
	}
	var report struct {
		Adapter *hostAdapterReport `json:"adapter"`
	}
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatal(err)
	}
	if report.Adapter == nil || report.Adapter.Name != "claude" || report.Adapter.Source != string(claude.NoBinding) {
		t.Fatalf("status = %s; want the unbound adapter serving this host", stdout)
	}
}

// TestStatusOmitsTheAdapterRatherThanReportingAnEmptyOne pins the wire shape the
// skill routes on: absent means no adapter, and a present-but-empty object
// would be a fourth state nothing produces and no rule covers.
func TestStatusOmitsTheAdapterRatherThanReportingAnEmptyOne(t *testing.T) {
	encoded := encodeStatus(t, statusReport{})
	if strings.Contains(encoded, "adapter") {
		t.Fatalf("status = %s; want no adapter key when nothing serves the host", encoded)
	}
	encoded = encodeStatus(t, statusReport{Adapter: &hostAdapterReport{Name: "claude", Source: string(claude.NoBinding)}})
	if !strings.Contains(encoded, `"source": "unbound"`) {
		t.Fatalf("status = %s; want the unbound source reported", encoded)
	}
}

func encodeStatus(t *testing.T, report statusReport) string {
	t.Helper()
	var encoded strings.Builder
	if err := conductor.WriteJSON(&encoded, report); err != nil {
		t.Fatal(err)
	}
	return encoded.String()
}

// workInTemporaryDirectory moves the process somewhere with no identity file
// above it, so the test observes what it wrote rather than the repository the
// suite happens to run in.
func workInTemporaryDirectory(t *testing.T) string {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	working := t.TempDir()
	if err := os.Chdir(working); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatal(err)
		}
	})
	// TempDir hands back a path that may traverse a symlink on this host, and
	// the identity walk resolves what it is given -- so compare like with like.
	resolved, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

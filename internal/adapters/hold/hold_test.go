package hold

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/platform"
	"github.com/danlavee/Conductor/internal/state"
)

const adapter = "test"

func TestWatchAcknowledgesWhatItHandsOver(t *testing.T) {
	client := joined(t)
	var got state.Batch
	delivered, err := Watch(context.Background(), client, live(t), func(batch state.Batch) ([]state.ReadResult, error) {
		got = batch
		return batch.Deliveries, nil
	})
	if err != nil || !delivered || len(got.Deliveries) == 0 {
		t.Fatalf("delivered=%v, %v, batch %+v", delivered, err, got)
	}
	assertQuiet(t, client)
}

// A watcher whose session is gone hands nothing over, so nothing is
// acknowledged and the next watcher still receives the work.
func TestWatchLeavesWorkForASuccessorWhenItsTargetIsGone(t *testing.T) {
	client := joined(t)
	orphan := live(t)
	orphan.Target = exitedProcess(t)
	delivered, err := Watch(context.Background(), client, orphan, refuse(t))
	if err != nil || delivered {
		t.Fatalf("orphan delivered=%v, %v", delivered, err)
	}
	drain(t, client)
}

func TestWatchAcknowledgesOnlyWhatTheHostTook(t *testing.T) {
	client := joined(t)
	deliver := func(batch state.Batch) ([]state.ReadResult, error) {
		if len(batch.Deliveries) != 2 {
			t.Errorf("batch has %d deliveries, want the roster line and the post", len(batch.Deliveries))
		}
		return batch.Deliveries[:1], nil
	}
	if delivered, err := Watch(context.Background(), client, live(t), deliver); err != nil || !delivered {
		t.Fatalf("delivered=%v, %v", delivered, err)
	}

	failure := errors.New("host refused")
	delivered, err := Watch(context.Background(), client, live(t), func(batch state.Batch) ([]state.ReadResult, error) {
		if len(batch.Deliveries) != 1 || batch.Deliveries[0].Topic != "messages/team" {
			t.Errorf("redelivered %+v, want only the post the host did not take", batch.Deliveries)
		}
		return nil, failure
	})
	if !errors.Is(err, failure) || delivered {
		t.Fatalf("failed hand-over delivered=%v, %v", delivered, err)
	}
	if _, waker := records(client); !exists(waker) {
		t.Fatal("a failed hand-over removed the waker record")
	}
	drain(t, client)
	assertQuiet(t, client)
}

func TestOnlyOneWatcherServesAnAgent(t *testing.T) {
	client := joined(t)
	drain(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	first := start(func() (bool, error) { return Watch(ctx, client, live(t), refuse(t)) })
	_, waker := records(client)
	waitFor(t, "the first watcher to register", func() bool { return exists(waker) })

	if delivered, err := Watch(context.Background(), client, live(t), refuse(t)); err != nil || delivered {
		t.Fatalf("second watcher delivered=%v, %v", delivered, err)
	}
	cancel()
	if result := await(t, first); result.err != nil || result.delivered {
		t.Fatalf("first watcher = %+v", result)
	}
}

// Rebinding or forgetting the session, or the agent joining in another session,
// ends both roles, and the guard returns.
func TestTheSessionEndingReleasesHolderAndWatcher(t *testing.T) {
	for name, end := range map[string]func(root string) error{
		"rebind": func(root string) error { _, _, err := binding.Bind(root, adapter, "s1", "peer"); return err },
		"forget": func(root string) error { return binding.Forget(root, adapter, "s1") },
		"taken over": func(root string) error {
			if err := binding.Serve(root, "other-host", "s2"); err != nil {
				return err
			}
			_, _, err := binding.Bind(root, "other-host", "s2", "planner")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := joined(t)
			drain(t, client)
			watcher := start(func() (bool, error) { return Watch(context.Background(), client, live(t), refuse(t)) })
			holder := start(func() (bool, error) { return false, Hold(context.Background(), client, live(t), nil) })
			holding(t, client)
			if err := end(client.Home); err != nil {
				t.Fatal(err)
			}
			for role, result := range map[string]chan result{"watcher": watcher, "holder": holder} {
				if r := await(t, result); r.err != nil || r.delivered {
					t.Fatalf("%s = %+v", role, r)
				}
			}
			if guardHeld(t, client) {
				t.Fatal("the guard outlived the hold")
			}
		})
	}
}

// After a delivery the waker record is gone and a turn is running, so the
// holder keeps the agent wakeable until that turn's end arms the next watcher.
func TestHoldOutlivesADeliveredTurn(t *testing.T) {
	client := joined(t)
	ctx, cancel := context.WithCancel(context.Background())
	holder := start(func() (bool, error) { return false, Hold(ctx, client, live(t), nil) })
	holding(t, client)
	drain(t, client)
	time.Sleep(20 * client.PollInterval)
	if !guardHeld(t, client) {
		t.Fatal("the holder let go while the delivered turn ran")
	}
	cancel()
	if r := await(t, holder); r.err != nil {
		t.Fatal(r.err)
	}
}

func TestHoldReleasesWhenItsWatcherDiedWithoutHandingOver(t *testing.T) {
	client := joined(t)
	_, waker := records(client)
	if err := writeWaker(waker, self(t)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	holder := start(func() (bool, error) { return false, Hold(ctx, client, live(t), nil) })
	holding(t, client)
	time.Sleep(20 * client.PollInterval)
	if !guardHeld(t, client) {
		t.Fatal("the holder let go of a live watcher")
	}

	if err := writeWaker(waker, exitedProcess(t)); err != nil {
		t.Fatal(err)
	}
	if r := await(t, holder); r.err != nil {
		t.Fatal(r.err)
	}
	cancel()
	if guardHeld(t, client) {
		t.Fatal("the guard outlived the hold")
	}
	// The next watcher overwrites the record; a holder that deleted it could
	// delete that watcher's registration instead.
	if !exists(waker) {
		t.Fatal("the holder removed the waker record")
	}
}

// A watcher's new holder can find the old one still holding: the old one may
// have read the dead record this watcher just replaced, and be releasing. The
// new holder waits while the record names a live watcher, so one of them holds.
func TestANewHolderTakesOverFromOneReleasing(t *testing.T) {
	client := joined(t)
	_, waker := records(client)
	if err := writeWaker(waker, self(t)); err != nil {
		t.Fatal(err)
	}
	oldCtx, releaseOld := context.WithCancel(context.Background())
	old := start(func() (bool, error) { return false, Hold(oldCtx, client, live(t), nil) })
	holding(t, client)

	newCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	successor := start(func() (bool, error) { return false, Hold(newCtx, open(t, client.Home, "planner"), live(t), nil) })
	time.Sleep(20 * client.PollInterval)
	select {
	case r := <-successor:
		t.Fatalf("the new holder gave up while a watcher lived: %+v", r)
	default:
	}

	// Cancelling stands in for the old holder's release on the stale read,
	// which no test can interleave deterministically.
	releaseOld()
	if r := await(t, old); r.err != nil {
		t.Fatal(r.err)
	}
	waitFor(t, "the new holder to take the guard", func() bool { return guardHeld(t, client) })
	cancel()
	if r := await(t, successor); r.err != nil {
		t.Fatal(r.err)
	}
}

// An in-process holder's watcher dies with it, so a dead record some earlier
// process left behind must not release it.
func TestAnInProcessHoldIgnoresAStaleWakerRecord(t *testing.T) {
	client := joined(t)
	_, waker := records(client)
	if err := writeWaker(waker, exitedProcess(t)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serve := func(ctx context.Context) error { <-ctx.Done(); return nil }
	holder := start(func() (bool, error) { return false, Hold(ctx, client, live(t), serve) })
	holding(t, client)
	time.Sleep(20 * client.PollInterval)
	if !guardHeld(t, client) {
		t.Fatal("a stale waker record released an in-process holder")
	}
	cancel()
	if r := await(t, holder); r.err != nil {
		t.Fatal(r.err)
	}
}

func TestHoldHasNothingToHold(t *testing.T) {
	client := joined(t)
	release, err := open(t, client.Home, "planner").AcquireWatchOwnership(state.HolderSelf, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Hold(context.Background(), client, live(t), nil); err != nil {
		t.Errorf("locked: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}

	unbound := live(t)
	unbound.ID = "never-served"
	if err := Hold(context.Background(), client, unbound, nil); err != nil {
		t.Errorf("unbound: %v", err)
	}

	if _, _, err := binding.Bind(client.Home, adapter, "s1", "ghost"); err != nil {
		t.Fatal(err)
	}
	if err := Hold(context.Background(), open(t, client.Home, "ghost"), live(t), nil); err != nil {
		t.Errorf("unregistered: %v", err)
	}
}

// An ancestor target is the process it stops at, recorded as ProcessOf would.
func TestAncestorTargetStopsAtTheFirstUnskippedProcess(t *testing.T) {
	if _, err := platform.InspectProcess(os.Getpid()); err != nil {
		t.Skip(err)
	}
	for want, skip := range map[platform.Process]func(platform.ProcessInstance) bool{
		self(t):                   func(platform.ProcessInstance) bool { return false },
		targetOf(t, os.Getppid()): func(p platform.ProcessInstance) bool { return p.PID == os.Getpid() },
	} {
		if got, err := AncestorTarget(os.Getpid(), skip); err != nil || got != want {
			t.Errorf("AncestorTarget = %+v, %v; want %+v", got, err, want)
		}
	}
}

func TestInjectAcknowledgesOnlyWhatItShowed(t *testing.T) {
	client := joined(t)
	if delivered, err := Inject(client, live(t), func(batch state.Batch) ([]state.ReadResult, error) {
		return batch.Deliveries[:1], nil
	}); err != nil || !delivered {
		t.Fatalf("first inject delivered=%v, %v", delivered, err)
	}
	if delivered, err := Inject(client, live(t), func(batch state.Batch) ([]state.ReadResult, error) {
		if len(batch.Deliveries) != 1 || batch.Deliveries[0].Topic != "messages/team" {
			t.Errorf("second inject got %+v, want only what the first did not show", batch.Deliveries)
		}
		return batch.Deliveries, nil
	}); err != nil || !delivered {
		t.Fatalf("second inject delivered=%v, %v", delivered, err)
	}
	for range 2 { // the second answers from the heads the first kept
		if delivered, err := Inject(client, live(t), refuse(t)); err != nil || delivered {
			t.Fatalf("an inject with nothing unread delivered=%v, %v", delivered, err)
		}
	}
	if _, err := open(t, client.Home, "peer").Put("messages/team", "later"); err != nil {
		t.Fatal(err)
	}
	if delivered, err := Inject(client, live(t), func(batch state.Batch) ([]state.ReadResult, error) {
		return batch.Deliveries, nil
	}); err != nil || !delivered {
		t.Fatalf("an inject after the kept heads moved delivered=%v, %v", delivered, err)
	}
	assertQuiet(t, client)
}

// A hook must not outwait its host's timeout: on a held session, or one no
// longer bound to the agent, inject shows nothing and leaves the work unread.
func TestInjectShowsNothingItCannotSafelyHandOver(t *testing.T) {
	client := joined(t)
	release, err := platform.AcquireFileMutex(sessionGuard(client), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if _, err := Inject(client, live(t), refuse(t)); err != nil || time.Since(began) > InjectWait+time.Second {
		t.Fatalf("inject on a held session = %v after %v", err, time.Since(began))
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := binding.Forget(client.Home, adapter, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Inject(client, live(t), refuse(t)); err != nil {
		t.Fatalf("inject in an unbound session = %v", err)
	}
	if err := binding.Serve(client.Home, adapter, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := binding.Bind(client.Home, adapter, "s1", "planner"); err != nil {
		t.Fatal(err)
	}
	drain(t, client)
}

// What an inject showed while the watcher waited for the session is not shown
// again: the watcher re-reads under the session's lock, finds nothing, and
// waits on, still registered, for the next publication.
func TestAWatcherDoesNotRedeliverWhatAnInjectShowed(t *testing.T) {
	client := joined(t)
	release, err := platform.AcquireFileMutex(sessionGuard(client), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var got state.Batch
	watcher := start(func() (bool, error) {
		return Watch(context.Background(), client, live(t), func(batch state.Batch) ([]state.ReadResult, error) {
			got = batch
			return batch.Deliveries, nil
		})
	})
	_, waker := records(client)
	waitFor(t, "the watcher to register", func() bool { return exists(waker) })
	time.Sleep(10 * client.PollInterval) // past its wait, blocked on the session
	unread, err := client.Unread()
	if err != nil || client.AcknowledgeRead(unread.Deliveries...) != nil {
		t.Fatalf("the stand-in inject failed: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-watcher:
		t.Fatalf("the watcher returned on work already shown: %+v, %+v", r, got)
	case <-time.After(20 * client.PollInterval):
	}
	if !exists(waker) {
		t.Fatal("the watcher gave up its registration")
	}
	if _, err := open(t, client.Home, "peer").Put("messages/team", "later"); err != nil {
		t.Fatal(err)
	}
	if r := await(t, watcher); r.err != nil || !r.delivered {
		t.Fatalf("watcher = %+v", r)
	}
	if len(got.Deliveries) != 1 || got.Deliveries[0].Publications[0].Records[0].Text != "later" {
		t.Fatalf("watcher delivered %+v, want only the later post", got.Deliveries)
	}
}

func sessionGuard(client *state.Client) string {
	return filepath.Join(binding.Directory(client.Home, adapter), "sessions", "s1.guard")
}

// joined is the planner, subscribed to messages/team and bound to session s1,
// with pending work from a peer: its roster line and one post.
func joined(t *testing.T) *state.Client {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	planner, err := state.New(root, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Join("planner", "plans"); err != nil {
		t.Fatal(err)
	}
	if _, err := planner.SubscribeTopic("messages/team"); err != nil {
		t.Fatal(err)
	}
	if err := binding.Serve(root, adapter, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := binding.Bind(root, adapter, "s1", "planner"); err != nil {
		t.Fatal(err)
	}
	peer := open(t, root, "peer")
	if _, err := peer.Join("peer", "reviews"); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Put("messages/team", "hello"); err != nil {
		t.Fatal(err)
	}
	return open(t, root, "planner")
}

func open(t *testing.T, root, agent string) *state.Client {
	t.Helper()
	client, err := state.Open(root, agent)
	if err != nil {
		t.Fatal(err)
	}
	client.PollInterval = 10 * time.Millisecond
	return client
}

// self is this test process as a target.
func self(t *testing.T) platform.Process { return targetOf(t, os.Getpid()) }

func targetOf(t *testing.T, pid int) platform.Process {
	t.Helper()
	target, err := platform.ProcessOf(pid)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// live is session s1 hosted by this test process.
func live(t *testing.T) Session {
	return Session{Binding: adapter, ID: "s1", Target: self(t)}
}

// exitedProcess names a process that has certainly run and certainly finished.
func exitedProcess(t *testing.T) platform.Process {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^$")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	target := targetOf(t, command.Process.Pid)
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	return target
}

func drain(t *testing.T, client *state.Client) {
	t.Helper()
	// Watchers here share this process, so one that returned without handing
	// over leaves a record naming a live process; a real one exits with it.
	_, waker := records(client)
	if err := platform.Remove(waker); err != nil {
		t.Fatal(err)
	}
	delivered, err := Watch(context.Background(), client, live(t), func(batch state.Batch) ([]state.ReadResult, error) {
		return batch.Deliveries, nil
	})
	if err != nil || !delivered {
		t.Fatalf("drain delivered=%v, %v", delivered, err)
	}
}

// assertQuiet shows the cursor covers everything: a watcher finds nothing.
func assertQuiet(t *testing.T, client *state.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if delivered, err := Watch(ctx, client, live(t), refuse(t)); err != nil || delivered {
		t.Fatalf("quiet watch delivered=%v, %v", delivered, err)
	}
}

func refuse(t *testing.T) func(state.Batch) ([]state.ReadResult, error) {
	return func(batch state.Batch) ([]state.ReadResult, error) {
		t.Errorf("unexpected delivery %+v", batch)
		return nil, errors.New("unexpected delivery")
	}
}

// holding waits for the holder's owner record, rather than probing the guard,
// which a holder still starting would read as LOCKED and decline.
func holding(t *testing.T, client *state.Client) {
	t.Helper()
	owner := filepath.Join(client.Home, "state", "watch", client.Agent+".owner.json")
	waitFor(t, "the holder to take the guard", func() bool { return exists(owner) })
}

// guardHeld probes the guard; only safe once the holder holds or has returned.
func guardHeld(t *testing.T, client *state.Client) bool {
	t.Helper()
	release, err := open(t, client.Home, client.Agent).AcquireWatchOwnership(state.HolderSelf, "")
	var protocol *state.ProtocolError
	if errors.As(err, &protocol) && protocol.Code == "LOCKED" {
		return true
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	return false
}

type result struct {
	delivered bool
	err       error
}

// start runs a role in the background; its result arrives on the channel, so
// a role that hangs fails the test at await's timeout rather than panicking later.
func start(role func() (bool, error)) chan result {
	done := make(chan result, 1)
	go func() {
		delivered, err := role()
		done <- result{delivered, err}
	}()
	return done
}

func await(t *testing.T, done chan result) result {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("role did not return within 5s")
		return result{}
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

# Claude adapter

Status: built on this branch, at [`adapters/claude-code/`](../../adapters/claude-code/README.md). The wake it rests on — a backgrounded hook exiting 2 starts a turn in an idle session — has been run end to end against a live session: it woke an idle session with a delivery payload on stdout, drained a backlog, and re-armed itself between wakes with no human input. See [verified wake methods](verified-wake-methods.md) for the runs and the evidence standard. Those runs predate the split into a holder and a watcher described below, which so far is proved by unit and process tests only. Coverage was also partial: the lifecycle trigger was exercised only on `startup`, the turn-boundary trigger never by a real `Stop` hook, teardown never by a real `SessionEnd`.

Packaged as a Claude Code plugin: `.claude-plugin/plugin.json`, with `hooks/hooks.json` and a marketplace manifest beside it. Installing the plugin and binding an identity is the whole setup — there is no per-session step and nothing for the agent to run.

`conductor adapter claude install <dir>` places the tree and the executable together, so the two cannot disagree about where the binary is. Enabling the plugin stays a host command, because the host's plugin registry is private state Conductor declines to write; see [the adapter's README](../../adapters/claude-code/README.md) for the two commands.

## Holder and watcher

Two processes serve a bound session, and the hold is shared by every adapter; only the hooks and the exit code are this host's.

The **watcher** is `arm`, a background hook marked `asyncRewake`. It takes the agent's watch lock under `<root>/adapters/watch/`, writes a waker record naming itself, spawns the holder, and blocks on the stream. On a delivery it re-reads what is unread under the session's lock, prints the plain-text wake to stdout, removes its waker record, acknowledges exactly what it printed, and exits 2; that output reaches the model as a system reminder, waking an idle session with no active turn. In a running turn the host queues it and attaches it whole after the next tool result, in the same turn (live check, Claude Code 2.1.216). It is the only process that acknowledges anything.

`SessionStart`, `Stop` and `PostToolUse` all run `arm`. The `PostToolUse` one is for a turn a wake started: its watcher exited to start it, so without one armed at a tool result, what arrived during that turn would wait for its end. An `arm` that finds a live watcher's record exits at once, so arming on every tool result costs a process start. `PostToolUse` also fires inside subagents, with the parent's `session_id`; `arm` ignores an event carrying `agent_id`, so a subagent never arms the parent's watcher. Live gates: that an `asyncRewake` `PostToolUse` hook rewakes as a `Stop` one does, and that a subagent's tool use does not arm.

The **holder** is `conductor adapter claude hold --session=<id> --target=<pid:start>`, started detached by the watcher. It owns the identity's watch guard — what the roster reports as `"holder": "adapter"` — and acknowledges nothing. Because it is not a hook, the hook timeout does not kill it and teardown does not wait for it, so it keeps the agent wakeable through the woken turn, after the watcher has exited and before the turn's end arms the next one.

The holder releases on the first of:

- the target — the session process — is gone;
- the session's binding no longer names the agent, which covers both a rebind and teardown;
- the waker record names a process that is not alive: a watcher ended without handing a delivery over, so no turn is coming to arm a successor.

A missing waker record is the opposite case: the last watcher handed a delivery over, a turn is running, and its end arms the next watcher, so the holder keeps holding. The holder never removes the record, so it cannot erase a live watcher's registration; and a new holder that finds the guard still held waits while the record names a live watcher, because the owner may be an older holder releasing on the record that watcher just replaced.

## The four responsibilities

Each is one hook entry, and all of them run the same executable: `conductor adapter claude <arm|release|identity>`, with the hook's event on stdin. That command tree is where this host's knowledge lives. It consumes the same client the CLI uses, so the core learns nothing about hooks, sessions, or exit codes.

| Responsibility | Realization |
| --- | --- |
| Resident component | The holder, which `arm` starts. |
| Turn-boundary trigger | A `Stop` hook running `arm`, the watcher. It backgrounds without blocking the turn, and every turn end arms the next watcher. |
| Lifecycle trigger | A `SessionStart` hook matching `startup`, `resume`, `clear`, `compact`, and `fork`. Runs `identity`, which serves the session and announces its binding into context, then `arm`. |
| Teardown | A `SessionEnd` hook running `release`, which forgets the session's binding. The watcher and the holder see it gone at their next poll and exit. Not optional: the host awaits outstanding rewake hooks during session teardown, so a watcher that ignored session end would delay the host's exit. |

`release` is scoped to its session: it forgets that session's binding and no other, so a second session in the same project keeps its own. Bindings live under `<root>/adapters/claude/sessions/`, and the watch lock and waker record under `<root>/adapters/watch/` — outside the protocol's own state, which has no concept of a host session and should not acquire one.

## Which session a hook is in

Measured on Claude Code 2.1.216, plugin and settings hooks alike, and read from the binary of the Desktop's bundled 2.1.317. In the live gate of 2026-11-07, hooks passing `--session=${CLAUDE_SESSION_ID}` failed on every event with the placeholder unsubstituted, while the hook's environment and stdin carried the real session:

| Where | What it carries |
| --- | --- |
| A hook's `command` and `args` | Substitution of `${CLAUDE_PROJECT_DIR}`, `${CLAUDE_PLUGIN_ROOT}`, `${CLAUDE_PLUGIN_DATA}` and `${user_config.*}` only. `${CLAUDE_SESSION_ID}` arrives literally; the host substitutes it only in skill and command prompt text. |
| A hook's environment | `CLAUDE_CODE_SESSION_ID`, the session, and `CLAUDE_PID`, the session process. Processes the model starts also get `CLAUDE_CODE_SESSION_ID`. |
| A hook's stdin | The event as JSON, with `session_id` on every event and `agent_id` inside a subagent. |

The adapter takes the session from the event's `session_id`: it is the field the event itself defines, and every hook already reads its event. A missing, empty or unusable `session_id` exits non-zero rather than degrading. Every session would otherwise share one identifier, and one session's `SessionEnd` would forget the binding every session is using — the version of that fault which shipped was invisible precisely because it exited cleanly, and the next shipped a `--session=${CLAUDE_SESSION_ID}` argument that no version substitutes.

The hooks stay in exec form so that a hook's parent is the session process, which the watcher records as the holder's target. `CLAUDE_PID` names that process without relying on the spawn form, and is an option if exec form ever has to go.

## Whether an adapter is here at all

The plugin lives in the host's registry, which is private state nothing here may read, so installation cannot be observed — and the environment a session exports says a Claude Code session is running, not that the plugin is. What the agent observes instead is the adapter's own work: the `SessionStart` announcement in its context, and `"holder": "adapter"` in its roster entry once the holder runs. A session that sees neither owns its own reachability.

## Binding a session to an agent

A session's binding is one file under `<root>/adapters/claude/sessions/`, keyed by session identifier. `SessionStart`'s `identity` hook creates it empty — the session is *served* — and `join`, run by the model inside that session, finds it through the exported `CLAUDE_CODE_SESSION_ID`, writes the agent's name into it, and answers `"wake": "adapter"`. Teardown removes it. A join from a session no hook served answers `"watch"`, so the model holds its own stream exactly where nothing else will.

The binding is made by the act that registers the name, so it cannot name an agent that has not joined, needs no per-project file, and lets any number of sessions in one directory act as different agents. A later join under another name rebinds the session, and the old name's holder and watcher release on it.

An `arm` that finds another watcher live for the identity exits cleanly having done nothing, and a holder that finds the guard held waits only while a live watcher may need it, then exits. That is what makes it safe for session start and every turn end to arm blindly, and the property the design rests on: every path that could lose the watcher has an event that restores it, and no restore can double-deliver.

One process wakes once, because delivering the wake *is* exiting. The successor is always started by an event rather than by the process that just fired — a delivery wakes the session, the resulting turn ends, and the `Stop` hook arms the next watcher. Session start, resume, and compaction enter the same cycle.

Writing to stdout and waking are one decision. Stdout reaches the model only on a waking exit, so only a delivery writes, and it exits 2. The wake is also decided before any error is: once the payload is on stdout, a later failure such as an acknowledgement that did not land must not convert a completed delivery into a silent clean exit. Waking anyway costs a duplicate; not waking costs the turn.

In a session that has not joined, `arm` does nothing and exits cleanly; the turn end after its join arms the watcher.

The exit status is `0` for nothing to say, `1` for a genuine failure, and `2` for a wake. This host reads any other status as a hook failure and shows its stderr to the user, so a blind re-arm that declines exits `0`.

## Constraints that shape the implementation

A backgrounded `asyncRewake` hook is not blocking anything: its own stdout is appended to the system reminder, so the delivery payload is simply what the watcher prints before exiting 2.

The reminder's default prefix reads as a Stop-hook blocking error, which misdescribes a delivery. `rewakeMessage` overrides that prefix and `rewakeSummary` sets the line the user sees; a hook may also emit a JSON line on stdout carrying its own `rewakeSummary`, letting each delivery label itself. Both fields are marked internal in the host's schema, so the adapter must remain correct without them — they improve how a delivery reads, they do not carry it.

Backgrounding is conditional on the session being interactive or having streaming input. Under a plain one-shot `--print` invocation an `asyncRewake` hook does not background, which is consistent with there being no session to wake — but it does mean the mechanism cannot be exercised headlessly in that mode.

**A watcher lives exactly as long as its hook's `timeout`, and dies silently.** Measured, not inferred: with the timeout set to 150 seconds the hook was killed 150.3 seconds after arming, and with it set to 60 it died inside the corresponding window. The kill runs no exit path, so nothing is logged and no wake is emitted — and because the session is idle, no turn is ending and no event re-arms it. Its waker record then names a dead process, so the holder releases and the roster stops reporting the agent as wakeable rather than claiming a listener that is gone.

Command-hook timeouts are used directly with no upper clamp — only `SessionEnd` timeouts are clamped — so the adapter sets a deliberately long timeout and treats the watcher's lifetime as configuration rather than as something to rely on by default. A watcher that dies anyway is not a loss: deliveries persist, and the next turn boundary arms a watcher and drains the backlog. That is requirement 10 doing its job, and it is the reason the turn-boundary trigger has to exist even though the immediate-wake path works.

**Nothing may outlive the session it wakes, and no host event guarantees that.** The wake is stdout plus an exit code, so both are meaningless once the session process is gone — an orphan would write into nothing and then acknowledge, putting the publication beyond redelivery. `SessionEnd` covers the orderly exit only; a crash, a kill, or a one-shot `--print` process that simply exits fires nothing. So `arm` records the process that spawned it — the session itself, since hooks are spawned in exec form with no shell between — as the target, with its start token, and the watcher and the holder both end when it is gone, checked on the poll interval. A recycled PID, or a start token that can no longer be read, counts as gone: a dropped hold is restored at the next turn boundary, while an orphan destroys work. The watcher also re-checks the target before it hands a delivery over.

Post-compaction state must be re-announced through `SessionStart` with matcher `compact`. `PostCompact` is side-effect-only: it cannot inject context or block. Hook registrations themselves are unaffected by compaction, being configuration rather than conversation.

`identity` reports the bound `agent`, if any, and `advice` on how joining binds the session. It reports no stream state: an announcement says who you are, never how you are.

One identity is bound to one session, and the last join wins: a join from a second session takes the identity over, answers `took_over`, and the first session's holder and watcher release on the lost binding. Each new watcher may briefly overlap a spare holder, which exits once that watcher hands over.

Per-OS invocation needs no launcher script, which is why the adapter is a mode of the Conductor executable rather than a pair of shell scripts. Without an `args` array a hook's `command` runs through a shell — bash on POSIX, PowerShell on Windows without Git Bash — and the `shell` field's `powershell` option means pwsh, which need not be installed. A single portable hook entry is therefore only possible in exec form: `args` present, `command` resolved as an executable and spawned directly, with `${CLAUDE_PLUGIN_ROOT}` substituted per element as a plain string so a path containing quotes, `$`, or backticks never reaches a shell parser. One `${CLAUDE_PLUGIN_ROOT}/bin/conductor` covers every platform, since Windows process creation appends `.exe` to an extensionless name.

## First instance

Claude was the first host the [wake adapter design](../design/wake-adapters.md) was realized on, and the requirements numbered here are that design's.

Packaged as a plugin — `.claude-plugin/plugin.json` with `hooks/hooks.json` beside it. It lives at [`adapters/claude-code/`](../../adapters/claude-code/README.md); its host-specific logic is `conductor adapter claude <arm|release|identity>`.

| Responsibility | Realization |
| --- | --- |
| Resident component | The holder: a detached `adapter claude hold` process the watcher starts. It owns the stream's guard and acknowledges nothing. |
| Turn-boundary trigger | A `Stop` hook running `arm`, the watcher, marked `asyncRewake`. It backgrounds without blocking the turn, blocks on the stream, and exits 2 when a delivery arrives; its output reaches the model as a system reminder, and this wakes an idle session with no active turn, and lands after the next tool result in a running one. Every turn end, and every tool result, arms the next watcher if none is live, so a turn a wake started also receives what arrives during it. |
| Lifecycle trigger | A `SessionStart` hook matching `startup`, `resume`, `clear`, `compact`, and `fork`. Arms the watcher and re-announces state. |
| Teardown | A `SessionEnd` hook that forgets the session's binding, plus the holder and the watcher both watching the session process. The host awaits outstanding rewake hooks while tearing a session down, so a watcher that ignores session end delays the host's own exit — but `SessionEnd` covers only the orderly exit, and the hook's timeout is deliberately a day, so both must also end themselves when the session process is gone. |

Three findings shape this:

**No model participation is required at any point.** The host arms the watcher, the host wakes the session, the host re-arms after each wake. Requirement 11 is satisfied outright rather than approximated.

**Wake and re-arm form a closed cycle.** A delivery wakes the session; the resulting turn ends; the turn-boundary trigger arms the next watcher. Session start, resume, and compaction enter the same cycle. Every path that loses a watcher has an event that restores it.

**Holding is apart from watching.** The watcher wakes by exiting, but the agent must stay wakeable through the turn that wake starts, so the stream's guard is owned by the holder, which is not a hook: no hook timeout kills it and teardown does not wait for it. A waker record says which watcher is live. An agent is bound in one session at a time and the last join wins: every join, whether or not it binds its own session, unbinds every other session bound to the agent, under any adapter, and names the host it took the agent from. The holder releases when the session process is gone, when the binding no longer names the agent, or when that record names a watcher that died without handing a delivery over; a missing record means a delivery started a turn whose end arms the next watcher. [Holder and watcher](#holder-and-watcher) gives the rules.

### Verified

The capability the design stands on — that exiting 2 from a backgrounded hook starts a turn in a session sitting idle — has been observed in isolation, with no Conductor in the loop. Three arms, two wakes, no human input, the successor armed by the `Stop` hook at each turn end. The run is recorded in [verified wake methods](verified-wake-methods.md).

That confirms the arrangement and not only the mechanism. Because one process wakes at most once, and outstanding hooks are drained at session teardown rather than at turn end, the watcher and the turn-boundary trigger collapse into a single `asyncRewake` `Stop` hook — the successor is armed by the event that ends the woken turn, not by the process that fired.

A third finding was not anticipated and matters more than either: **a hook may have a lifetime imposed by its host, and may lose it silently.** Here the watcher is killed at its hook's configured timeout, running no exit path, emitting no wake, while the session sits idle with no event due to re-arm it. Its lifetime is therefore a property the adapter must know, extend where the host allows, and survive the loss of where it does not — which is what requirement 10 is for. The turn-boundary trigger is not a backstop for a flaky wake path; the wake path is reliable. It is the backstop for a watcher that can vanish without saying so; the waker record it leaves naming a dead process is what makes the holder release rather than report a listener that is gone.

A fourth finding is its mirror: **a resident component can also outlive its host, and nothing the bus can see distinguishes that from a healthy one.** An orphan holds the guard, wakes into a stdout nobody reads, and acknowledges — so the identity reports as reachable while every publication to it is destroyed on arrival. One-shot invocations reach this most easily, exiting without firing teardown, and the day-long timeout above is what makes it expensive. Requirement 11 closes it; teardown events alone do not satisfy it.

Still open: the holder and watcher split has not yet been run against a live session; whether the lifecycle trigger behaves the same on `resume`, `clear`, `compact`, and `fork` as it does on `startup`; and whether a wake can land in a session other than the one that armed it.

## Installation and enabling

Installation places host configuration but does not enable it. `conductor adapter claude install <dir>` writes the plugin tree and puts the executable at the path its hooks name, with the same staging, hashing and atomic publication the skill gets, so the placement is no longer a manual copy that can be done half-right.

Enabling stays a host command the user runs — deliberate, not unfinished. This host discovers plugins through private state of its own, an install registry and a marketplace index, both undocumented and versioned on the host's schedule; writing into them would make Conductor's correctness depend on a format nobody promised to keep. The payload ships a marketplace manifest so the remaining step is one documented command against a local directory. The same reasoning rules out writing hook registrations into the user's settings file: it would work, and it would mean Conductor editing a hand-maintained file it does not own, with no way to merge safely or to know what it overwrote.

## Ruled out

**Headless resume per signal.** Spawning a fresh process against a persisted session (`--print --resume`) delivers a signal to a *different, dormant* session. Pointed at the live session an agent is running in, it spawns a competing process against the same transcript with no attached terminal to approve tool calls, and stalls silently. This was the previous `--claude-cli` path; the adapter replaces it.

**Desktop without an open session.** There is no way to start a Desktop turn with no interactive session already open. URL handlers open or prefill a task without submitting a turn; internal session enumeration is not a public API. Session listing, window activation, and prompt prefill are not wake paths. The adapter covers Desktop only while a session is open — which is what the idle-wake hook addresses, since it wakes an idle open session.

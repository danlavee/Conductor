# Claude adapter

Status: built on this branch, at [`adapters/claude-code/`](../../adapters/claude-code/README.md). This adapter's binary has been run end to end against a live session: it woke an idle session with a delivery payload on stdout, drained a backlog, re-armed itself between wakes with no human input, and reported `unbound`, `refused`, `unregistered` and `not-owned` directly and `delivered` through a waking exit. See [verified wake methods](verified-wake-methods.md) for the runs and the evidence standard. What remains unverified is coverage, not viability: the lifecycle trigger was exercised only on `startup`, the turn-boundary trigger never by a real `Stop` hook, teardown never by a real `SessionEnd`.

Packaged as a Claude Code plugin: `.claude-plugin/plugin.json`, with `hooks/hooks.json`, a marketplace manifest, and an optional `.mcp.json` beside it. Installing the plugin and binding an identity is the whole setup — there is no per-session step and nothing for the agent to run.

`conductor adapter claude install <dir>` places the tree and the executable together, so the two cannot disagree about where the binary is. Enabling the plugin stays a host command, because the host's plugin registry is private state Conductor declines to write; see [the adapter's README](../../adapters/claude-code/README.md) for the two commands.

## The four responsibilities

Each is one hook entry, and all of them run the same executable: `conductor adapter claude <arm|release|identity> --session=${CLAUDE_SESSION_ID}`. That command tree is where this host's knowledge lives. It consumes the same client the CLI uses, so the core learns nothing about hooks, sessions, or exit codes.

| Responsibility | Realization | Verified |
| --- | --- | --- |
| Resident component | `adapter claude arm` as a background hook marked `asyncRewake`. It holds the identity's ownership guard, blocks on the stream, prints the delivery to stdout and exits 2; that output reaches the model as a system reminder, waking an idle session with no active turn. | Yes, with this binary |
| Turn-boundary trigger | A `Stop` hook running the same `arm` command. Arming and the resident component are the same act: it backgrounds without blocking the turn, and every turn end re-arms. | Mechanism yes, this binary no |
| Lifecycle trigger | A `SessionStart` hook matching `startup`, `resume`, `clear`, `compact`, and `fork`. Runs `identity`, which records that this adapter served the session and announces the binding into context, then `arm`. | Partial — `startup` only |
| Teardown | A `SessionEnd` hook running `release`. Not optional: the host awaits outstanding rewake hooks during session teardown, so a stream that ignores session end delays the host's exit. | No |

`release` is scoped to the session that armed the stream, so a second session in the same project cannot tear down the first one's residency. It reaches the stream through an adapter-owned residency record under `<root>/adapters/claude/` — outside the protocol's own state, which has no concept of a host session and should not acquire one.

Records the adapter writes are published by rename. Teardown rewrites a residency from a different process than the one polling it, and a reader that caught a truncated write could not tell it from a release — it would cancel a live stream.

## Which session a hook is in

The host exposes the session identifier two ways, to two different kinds of caller, and this adapter has both kinds:

| Caller | Mechanism |
| --- | --- |
| A hook | `${CLAUDE_SESSION_ID}`, substituted into the hook's `command` and `args` before the process is spawned |
| A process the model starts | `CLAUDE_CODE_SESSION_ID`, exported into the environment |

They are not interchangeable. `CLAUDE_SESSION_ID` is *not* an environment variable — the host only ever substitutes it as text — so a hook reading it with `getenv` receives the empty string in every session. Every session then shares one identifier and the scoping above silently stops holding: one session's `SessionEnd` tears down whatever stream is resident, including one another session armed. So hooks take the session from `--session=${CLAUDE_SESSION_ID}`, and a missing or unsubstituted value exits non-zero rather than degrading — the version of that fault which shipped was invisible precisely because it exited cleanly.

## Whether an adapter is here at all

The skill routes on `status`'s `adapter` field: an agent that sees one binds to it and leaves the stream to it, an agent that sees none owns its own reachability. So the field has to be able to say no, and on this host it could not.

Presence was read from `CLAUDE_CODE_SESSION_ID`, which says a Claude Code session is running — equally true of a host that never installed the plugin, and the skill and the plugin are separate installs with the onboarding path installing only the skill. Every such session was told an adapter served it, bound an identity nothing would read, and was told not to arm on the strength of it.

The plugin lives in the host's registry, which is private state nothing here may read, so installation cannot be observed. The adapter *running* can be: the `SessionStart` `identity` hook records the session it served, and `status` reports an adapter only for a session carrying that record. Nothing else may write it — a model-run `conductor adapter claude identity` reads the same environment and would be asserting the very fact the hook exists to observe. `bind` refuses in a session with no such record for the same reason it refuses an unregistered name.

Teardown clears the record for every served session, including the ones that bound nothing — which used to leave the release path at "nothing bound here" before it cleared anything at all.

A session that began under a build too old to record it reads as unserved and owns its own stream for the rest of its life. That is the whole cost, and it is nothing to migrate: the gap is a missing record in a live session, and it goes when the session does.

## Two bindings, and which wins

The identity a hook acts for is resolved in a fixed order:

1. **Session binding** — what a session recorded for itself, via `conductor adapter claude bind <agent>`. Stored under `<root>/adapters/claude/sessions/`, keyed by session identifier, and removed at teardown.
2. **Project binding** — `.conductor-agent` at the project root. The ordinary case, and the one that needs nothing from the model.

The session binding wins because it is the more specific claim: a session that has said who it is has said so about itself, while the project file speaks for every session that happens to open there. When a session binding answers, the project is not consulted at all — including its absence.

This is what lets several agents work in one directory. One identity holds one stream, so without a per-session binding a second session in the same project resolves to the same identity, loses the race for it, and is refused at every turn end while looking, from the outside, exactly like a working install. `identity` reports which of the two bindings answered, so a session acting as the wrong agent shows which file to correct.

`bind` is the one adapter command the model runs itself, and the only one with preconditions: the identity is on the roster, and the session is one a hook has served. `.conductor-agent` is written by hand before an agent joins and so cannot require registration, while `bind` is run by an agent that has joined and inside a session that has already started. Both refusals buy the same thing — an unknown name and an unserved session each write a record nothing ever reads, silently, for the life of the session.

Arming when a live stream already holds the identity is refused and exits cleanly. That is what makes it safe for session start and every turn end to arm blindly, and the property the design rests on: every path that could lose the stream has an event that restores it, and no restore attempt can double-deliver.

One process wakes once, because delivering the wake *is* exiting. The successor is always started by an event rather than by the process that just fired — a delivery wakes the session, the resulting turn ends, and the `Stop` hook arms the next stream. Session start, resume, and compaction enter the same cycle.

Writing to stdout and waking are one decision. Stdout reaches the model only on a waking exit, so an outcome that writes without waking is an outcome nobody hears; the one that writes, a delivery, exits 2. The wake is also decided before any error is: once the payload is on stdout, a later failure such as an acknowledgement that did not land must not convert a completed delivery into a silent clean exit. Waking anyway costs a duplicate; not waking costs the turn.

An identity that has not joined is reported and exits cleanly, since arming again is harmless once it does join. The cost: a `.conductor-agent` naming an agent that will *never* join — a typo, or a copied project — repeats that no-op indefinitely, reporting to stderr where a backgrounded hook's output is easy to miss. The adapter cannot distinguish "not yet" from "never"; `conductor <agent> status` can.

## Reading a run's result

Every non-waking run prints one JSON report to stderr carrying a stable `code`, so a run can be classified numerically instead of by matching prose:

| Code | Outcome | Meaning |
| --- | --- | --- |
| 10 | `unbound` | The project names no identity. The adapter is installed and opted out. |
| 11 | `delivered` | Work reached the model. Exits with the wake code. |
| 12 | `refused` | Another stream already owns the identity. The ordinary result of a blind re-arm. |
| 13 | `unregistered` | The identity has not joined, so there is nothing to hold a stream for. |
| 14 | `released` | The stream this session was holding ended. Reported under `release` when teardown ends it, and under `arm` when a blocked stream is cancelled by the teardown of the session that armed it — so read the outcome, not the command it arrived under. |
| 15 | `not-owned` | Teardown found no stream this session owned. |
| 16 | `orphaned` | The session this stream exists to wake is gone. The stream ends holding nothing and acknowledging nothing. |

The codes start at ten to stay clear of the process exit statuses, a separate and much smaller vocabulary: `0` for nothing to say, `1` for a genuine failure, `2` for a wake. The exit status deliberately does not distinguish outcomes, because this host reads any status other than `0` and `2` as a hook failure and shows its stderr to the user — so giving `refused` or `unregistered` a status of its own would report an error at every turn end that armed correctly. The blind re-arm has to be able to decline without looking broken, which is exactly when it is working.

## Constraints that shape the implementation

The two paths carry content differently, and conflating them is the easy mistake. A *blocking* `Stop` hook's `reason` is not shown to the model, so content there must travel in `hookSpecificOutput.additionalContext`. A *backgrounded* `asyncRewake` hook is not blocking anything: its own stdout is appended to the system reminder, so the delivery payload is simply what the stream prints before exiting 2.

The reminder's default prefix reads as a Stop-hook blocking error, which misdescribes a delivery. `rewakeMessage` overrides that prefix and `rewakeSummary` sets the line the user sees; a hook may also emit a JSON line on stdout carrying its own `rewakeSummary`, letting each delivery label itself. Both fields are marked internal in the host's schema, so the adapter must remain correct without them — they improve how a delivery reads, they do not carry it.

Backgrounding is conditional on the session being interactive or having streaming input. Under a plain one-shot `--print` invocation an `asyncRewake` hook does not background, which is consistent with there being no session to wake — but it does mean the mechanism cannot be exercised headlessly in that mode.

**A stream lives exactly as long as its hook's `timeout`, and dies silently.** Measured, not inferred: with the timeout set to 150 seconds the stream was killed 150.3 seconds after arming, and with it set to 60 it died inside the corresponding window. The kill runs no exit path in the stream, so nothing is logged and no wake is emitted — and because the session is idle, no turn is ending and no event re-arms it. The session stays healthy, stays idle, and is silently unwakeable from that moment.

This is the original production failure reproduced one layer down, so the adapter must not inherit it. Command-hook timeouts are used directly with no upper clamp — only `SessionEnd` timeouts are clamped — so the adapter sets a deliberately long timeout and treats stream lifetime as configuration rather than as something to rely on by default. A stream that dies anyway is not a loss: deliveries persist, and the next turn boundary re-arms and drains the backlog. That is requirement 10 doing its job, and it is the reason the turn-boundary trigger has to exist even though the immediate-wake path works.

**A stream must not outlive the session it wakes, and no host event guarantees that.** The wake is stdout plus an exit code, so both are meaningless once the session process is gone — an orphan writes into nothing. Worse, it would then acknowledge, putting the publication beyond redelivery, which only covers what was never acknowledged. `SessionEnd` covers the orderly exit only; a crash, a kill, or a one-shot `--print` process that simply exits fires nothing, and the day-long timeout above is then how long the orphan lives. So the stream records the process that spawned it — the session itself, since hooks are spawned in exec form with no shell between — and ends when that process is gone, checked on the same interval the watch loop already polls. A recorded start token settles PID reuse, and a token that cannot be re-read ends the stream rather than being read as absence of evidence: a dropped stream is restored at the next turn boundary, while an orphan destroys work.

Claude Code overrides a `Stop` hook after eight consecutive blocks without progress, and passes `stop_hook_active` on stdin so the hook can detect it. The adapter must report pending work it cannot drain within that ceiling rather than blocking indefinitely.

Post-compaction state must be re-announced through `SessionStart` with matcher `compact`. `PostCompact` is side-effect-only: it cannot inject context or block. Hook registrations themselves are unaffected by compaction, being configuration rather than conversation.

Identity binding is `.conductor-agent`, a project-owned file. Plugin user-configuration is deliberately not readable from project settings, so it cannot carry a per-project identity. A project with no such file loads the adapter and does nothing, which is the correct behaviour for a project that has not opted in.

**`${CLAUDE_PROJECT_DIR}` is a hook's answer and only a hook's.** The host substitutes it into hook arguments and does not export it to processes the model starts, so a command an agent runs reads it as empty — every session, every host. Resolving identity from it there reported `unbound` for a directory whose `.conductor-agent` named exactly that agent, and the documented remedy for `unbound` binds a session identity that outranks the project's: a silent wrong answer that invites the act which makes it permanent. So the two callers use two sources, neither falling back to the other. A hook is owed the variable and fails loudly without it, its own working directory being nothing in particular. A model-run `identity` walks up from its working directory for `.conductor-agent` — the same fact the host would have supplied.

`identity` reports `project` and `root` — the directory it found and the state root it read — on every outcome, `unbound` included. Without them `source` is a conclusion the caller cannot check: "nothing names you" and "I could not see what names you" are the same output, they are different problems, and `bind` overwrites the `source` that would have distinguished them. It reports no stream state: an announcement says who you are, never how you are, and there is nothing an agent would do differently either way.

One identity holds one stream, so two sessions sharing an identity contend for it: the first to arm holds it, the second is refused every time, and a delivery wakes whichever won. The second stays reachable — nothing is lost — but is not independently wakeable. Per-session bindings are how two sessions in one project avoid sharing an identity; making one identity wake several sessions is not something this host's mechanism supports.

Per-OS invocation needs no launcher script, which is why the adapter is a mode of the Conductor executable rather than a pair of shell scripts. Without an `args` array a hook's `command` runs through a shell — bash on POSIX, PowerShell on Windows without Git Bash — and the `shell` field's `powershell` option means pwsh, which need not be installed. A single portable hook entry is therefore only possible in exec form: `args` present, `command` resolved as an executable and spawned directly, with `${CLAUDE_PLUGIN_ROOT}` substituted per element as a plain string so a path containing quotes, `$`, or backticks never reaches a shell parser. One `${CLAUDE_PLUGIN_ROOT}/bin/conductor` covers every platform, since Windows process creation appends `.exe` to an extensionless name.

## The MCP server

Optional, and carries no wake responsibility. Its value is ergonomic: Conductor's operations as typed tools rather than shell invocations by absolute path, and permission approval once per server instead of per command shape. A stdio MCP server is not restarted automatically if it dies, which is a second reason not to make it load-bearing for reachability.

## Ruled out

**Server-initiated turns.** This host does have a first-class push path — an MCP server declaring an experimental channel capability can push a notification into the open session and start a turn. It requires a launch flag, a curated allowlist, and an Anthropic authentication backend, is unavailable on other model-hosting backends, and is documented as a research preview whose contract may change. It can replace the resident component's wake where available, but cannot carry the guarantee.

**Headless resume per signal.** Spawning a fresh process against a persisted session (`--print --resume`) delivers a signal to a *different, dormant* session. Pointed at the live session an agent is running in, it spawns a competing process against the same transcript with no attached terminal to approve tool calls, and stalls silently. This was the previous `--claude-cli` path; the adapter replaces it.

**Desktop without an open session.** There is no way to start a Desktop turn with no interactive session already open. URL handlers open or prefill a task without submitting a turn; internal session enumeration is not a public API. Session listing, window activation, and prompt prefill are not wake paths. The adapter covers Desktop only while a session is open — which is what the idle-wake hook addresses, since it wakes an idle open session.

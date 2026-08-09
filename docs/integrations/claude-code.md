# Claude adapter

Status: built on this branch, at [`adapters/claude-code/`](../../adapters/claude-code/README.md). The wake mechanism is verified — an idle session with no active turn was woken twice by a backgrounded hook exiting 2, re-arming itself between wakes with no human input. See [verified wake methods](verified-wake-methods.md) for the run and the evidence standard. This adapter's own binary has since been run end to end against a live session: it woke an idle session with a delivery payload on stdout, drained a backlog, reported codes 10, 13, 14 and 16 directly and 11 through a waking exit, and `status` flipped to unwakeable when its stream died. What remains unverified is coverage, not viability: the lifecycle trigger was exercised only on `startup`, the turn-boundary trigger was never driven by a real `Stop` hook, and teardown never by a real `SessionEnd`.

Packaged as a Claude Code plugin: `.claude-plugin/plugin.json`, with `hooks/hooks.json`, a marketplace manifest, and an optional `.mcp.json` beside it. Installing the plugin and binding an identity is the whole setup — there is no per-session step and nothing for the agent to run.

`conductor adapter claude install <dir>` places the tree and the executable together, so the two cannot disagree about where the binary is. Enabling the plugin stays a host command, because the host's plugin registry is private state Conductor declines to write; see [the adapter's README](../../adapters/claude-code/README.md) for the two commands.

## The four responsibilities

Each is one hook entry, and all of them run the same executable: `conductor adapter claude <arm|release|identity> --session=${CLAUDE_SESSION_ID}`. That command tree is where this host's knowledge lives. It consumes Conductor's public client exactly as any other caller would, so the core learns nothing about hooks, sessions, or exit codes.

| Responsibility | Realization | Verified |
| --- | --- | --- |
| Resident component | `adapter claude arm` as a background hook marked `asyncRewake`. It holds the identity's ownership guard, blocks on the stream, prints the delivery to stdout and exits 2; that output reaches the model as a system reminder, waking an idle session with no active turn. | Yes, with this binary |
| Turn-boundary trigger | A `Stop` hook running the same `arm` command. Arming and the resident component are the same act: it backgrounds without blocking the turn, and every turn end re-arms. | Mechanism yes, this binary no |
| Lifecycle trigger | A `SessionStart` hook matching `startup`, `resume`, `clear`, `compact`, and `fork`. Runs `identity` to announce the binding into context, then `arm`. | Partial — `startup` only |
| Teardown | A `SessionEnd` hook running `release`. Not optional: the host awaits outstanding rewake hooks during session teardown, so a stream that ignores session end delays the host's exit. | No |

`release` is scoped to the session that armed the stream, so a second session in the same project cannot tear down the first one's residency. It reaches the stream through an adapter-owned residency record beside the control directory — not through the protocol root, which has no concept of a host session and should not acquire one.

## Which session a hook is in

The host exposes the session identifier two ways, to two different kinds of caller, and this adapter has both kinds:

| Caller | Mechanism |
| --- | --- |
| A hook | `${CLAUDE_SESSION_ID}`, substituted into the hook's `command` and `args` before the process is spawned |
| A process the model starts | `CLAUDE_CODE_SESSION_ID`, exported into the environment |

They are not interchangeable, and the difference is not cosmetic. `CLAUDE_SESSION_ID` is *not* an environment variable — the host only ever substitutes it as text — so an adapter that reads it with `getenv` from a hook receives the empty string on every host, in every session, and reports nothing. Every session then shares one identifier, and the scoping described above silently stops holding: one session's `SessionEnd` tears down whatever stream is resident, including one another session armed.

This adapter shipped that defect and no longer has it. Hooks take the session from `--session=${CLAUDE_SESSION_ID}`, and a missing or unsubstituted value is a hard failure rather than a degraded mode — the exit is non-zero, which this host shows to the user. An adapter that cannot tell one session from another is misconfigured, and the version of that fault which shipped was invisible precisely because it exited cleanly.

## Two bindings, and which wins

The identity a hook acts for is resolved in a fixed order:

1. **Session binding** — what a session recorded for itself, via `conductor adapter claude bind <agent>`. Stored beside the control directory, keyed by session identifier, and removed at teardown.
2. **Project binding** — `.conductor-agent` at the project root. The ordinary case, and the one that needs nothing from the model.

The session binding wins because it is the more specific claim: a session that has said who it is has said so about itself, while the project file speaks for every session that happens to open there. When a session binding answers, the project is not consulted at all — including its absence.

This is what lets several agents work in one directory. One identity holds one stream, so without a per-session binding a second session in the same project resolves to the same identity, loses the race for it, and is refused at every turn end while looking, from the outside, exactly like a working install. `identity` reports which of the two bindings answered, so a session acting as the wrong agent shows which file to correct.

`bind` is the one adapter command the model runs itself, and the only one that requires the identity to be on the roster already. `.conductor-agent` is written by hand before an agent joins and so cannot require registration; `bind` is run by an agent that has joined, where an unknown name is a typo that would otherwise become a quiet unregistered no-op at every turn end for the life of the session.

Arming when a live stream already holds the identity is refused and exits cleanly, not treated as a failure. That is what makes it safe for session start and every turn end to arm blindly, and it is the property the whole design rests on: every path that could lose the stream has an event that restores it, and no restore attempt can double-deliver.

One process wakes once, because delivering the wake *is* exiting. The successor is therefore always started by an event rather than by the process that just fired — a delivery wakes the session, the resulting turn ends, and the `Stop` hook arms the next stream. Session start, resume, and compaction enter the same cycle.

Writing to stdout and waking are one decision, not two. On this host stdout reaches the model only on a waking exit, so an outcome that writes without waking is an outcome nobody hears. Two outcomes write: a delivery, and a replacement activation when the conductor is cut over beneath the stream. Both exit 2. The wake is also decided before any error is: once the payload is on stdout the session must be woken to read it, and a failure after that point — an acknowledgement that did not land, say — must not convert a completed delivery into a silent clean exit. Conductor redelivers what was never acknowledged, so waking anyway costs a duplicate, while not waking costs the turn.

An identity that has not joined is reported and exits cleanly, on the reasoning that arming again is harmless once it does join. The cost is worth naming: a `.conductor-agent` naming an agent that will *never* join — a typo, or a copied project — produces that same quiet no-op at every session start and turn end, indefinitely, and the report goes to stderr where a backgrounded hook's output is easy to miss. The adapter cannot distinguish "not yet" from "never", so `conductor <agent> status` is the way to tell them apart.

## Reading a run's result

Every non-waking run prints one JSON report to stderr carrying a stable `code`, so a run can be classified numerically instead of by matching prose:

| Code | Outcome | Meaning |
| --- | --- | --- |
| 10 | `unbound` | The project names no identity. The adapter is installed and opted out. |
| 11 | `delivered` | Work reached the model. Exits with the wake code. |
| 12 | `replaced` | The conductor was cut over beneath the stream. Exits with the wake code. |
| 13 | `refused` | Another stream already owns the identity. The ordinary result of a blind re-arm. |
| 14 | `unregistered` | The identity has not joined, so there is nothing to hold a stream for. |
| 15 | `released` | The stream this session was holding ended. Reported under `release` when teardown ends it, and under `arm` when a blocked stream is cancelled by the teardown of the session that armed it — so read the outcome, not the command it arrived under. |
| 16 | `not-owned` | Teardown found no stream this session owned. |

The codes start at ten to stay clear of the process exit statuses, which are a separate and much smaller vocabulary: `0` for nothing to say, `1` for a genuine failure, and `2` for a wake. The exit status deliberately does not distinguish the outcomes, because this host reads any status other than `0` and `2` as a hook failure and shows its stderr to the user — so giving `refused` or `unregistered` a status of its own would report an error at every turn end that armed correctly. The blind re-arm has to be able to decline without looking broken, which is exactly when it is working.

## Constraints that shape the implementation

The two paths carry content differently, and conflating them is the easy mistake. A *blocking* `Stop` hook's `reason` is not shown to the model, so content there must travel in `hookSpecificOutput.additionalContext`. A *backgrounded* `asyncRewake` hook is not blocking anything: its own stdout is appended to the system reminder, so the delivery payload is simply what the stream prints before exiting 2.

The reminder's default prefix reads as a Stop-hook blocking error, which misdescribes a delivery. `rewakeMessage` overrides that prefix and `rewakeSummary` sets the line the user sees; a hook may also emit a JSON line on stdout carrying its own `rewakeSummary`, letting each delivery label itself. Both fields are marked internal in the host's schema, so the adapter must remain correct without them — they improve how a delivery reads, they do not carry it.

Backgrounding is conditional on the session being interactive or having streaming input. Under a plain one-shot `--print` invocation an `asyncRewake` hook does not background, which is consistent with there being no session to wake — but it does mean the mechanism cannot be exercised headlessly in that mode.

**A stream lives exactly as long as its hook's `timeout`, and dies silently.** Measured, not inferred: with the timeout set to 150 seconds the stream was killed 150.3 seconds after arming, and with it set to 60 it died inside the corresponding window. The kill runs no exit path in the stream, so nothing is logged and no wake is emitted — and because the session is idle, no turn is ending and no event re-arms it. The session stays healthy, stays idle, and is silently unwakeable from that moment.

This is the original production failure reproduced one layer down, so the adapter must not inherit it. Command-hook timeouts are used directly with no upper clamp — only `SessionEnd` timeouts are clamped — so the adapter sets a deliberately long timeout and treats stream lifetime as configuration rather than as something to rely on by default. A stream that dies anyway is not a loss: deliveries persist, and the next turn boundary re-arms and drains the backlog. That is requirement 10 doing its job, and it is the reason the turn-boundary trigger has to exist even though the immediate-wake path works.

Claude Code overrides a `Stop` hook after eight consecutive blocks without progress, and passes `stop_hook_active` on stdin so the hook can detect it. The adapter must report pending work it cannot drain within that ceiling rather than blocking indefinitely.

Post-compaction state must be re-announced through `SessionStart` with matcher `compact`. `PostCompact` is side-effect-only: it cannot inject context or block. Hook registrations themselves are unaffected by compaction, being configuration rather than conversation.

Identity binding is `.conductor-agent`, a project-owned file the adapter reads through `${CLAUDE_PROJECT_DIR}`. Plugin user-configuration is deliberately not readable from project settings, so it cannot carry a per-project identity. A project with no such file loads the adapter and does nothing, which is the correct behaviour for a project that has not opted in.

One identity holds one stream. Two sessions sharing an identity therefore contend for it: the first to arm holds it, the second's arm is refused every time, and a delivery wakes whichever one won. The second stays reachable — nothing is lost — but it is not independently wakeable. Per-session bindings are how two sessions in one project avoid sharing an identity in the first place; making one identity wake several sessions is not something this host's mechanism supports, and no binding changes that.

Per-OS invocation needs no launcher script, and this is why the adapter is a mode of the Conductor executable rather than a pair of shell scripts. Without an `args` array a hook's `command` runs through a shell — bash on POSIX, PowerShell on Windows without Git Bash — and the `shell` field's `powershell` option means pwsh, which need not be installed. A single portable hook entry is therefore only possible in the exec form: `args` present, `command` resolved as an executable and spawned directly, with `${CLAUDE_PLUGIN_ROOT}` substituted per element as a plain string so a path containing quotes, `$`, or backticks never reaches a shell parser. One `${CLAUDE_PLUGIN_ROOT}/bin/conductor` covers every platform, since Windows process creation appends `.exe` to an extensionless name.

## The MCP server

Optional, and carries no wake responsibility. Its value is ergonomic: Conductor's operations as typed tools rather than shell invocations by absolute path, and permission approval once per server instead of per command shape. A stdio MCP server is not restarted automatically if it dies, which is a second reason not to make it load-bearing for reachability.

## Ruled out

**Server-initiated turns.** This host does have a first-class push path — an MCP server declaring an experimental channel capability can push a notification into the open session and start a turn. It requires a launch flag, a curated allowlist, and an Anthropic authentication backend, is unavailable on other model-hosting backends, and is documented as a research preview whose contract may change. It can replace the resident component's wake where available, but cannot carry the guarantee.

**Headless resume per signal.** Spawning a fresh process against a persisted session (`--print --resume`) delivers a signal to a *different, dormant* session. Pointed at the live session an agent is running in, it spawns a competing process against the same transcript with no attached terminal to approve tool calls, and stalls silently. This was the previous `--claude-cli` path; the adapter replaces it.

**Desktop without an open session.** There is no way to start a Desktop turn with no interactive session already open. URL handlers open or prefill a task without submitting a turn; internal session enumeration is not a public API. Session listing, window activation, and prompt prefill are not wake paths. The adapter covers Desktop only while a session is open — which is what the idle-wake hook addresses, since it wakes an idle open session.

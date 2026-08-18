# Claude adapter

Status: designed, not built. The wake mechanism is verified — an idle session with no active turn was woken twice by a backgrounded hook exiting 2, re-arming itself between wakes with no human input. See [verified wake methods](verified-wake-methods.md) for the run and the evidence standard. What remains unverified is coverage, not viability: the lifecycle trigger was exercised only on `startup`, and teardown not at all.

Packaged as a Claude Code plugin: `.claude-plugin/plugin.json`, with `hooks/hooks.json` and an optional `.mcp.json` beside it. Installing the plugin and binding an identity is the whole setup — there is no per-session step and nothing for the agent to run.

## The four responsibilities

| Responsibility | Realization | Verified |
| --- | --- | --- |
| Resident component | A background hook holding the delivery stream, marked `asyncRewake`. It blocks, and exits 2 when a delivery arrives; its output reaches the model as a system reminder, waking an idle session with no active turn. | Yes |
| Turn-boundary trigger | A `Stop` hook, itself `asyncRewake`. Arming and the resident component are the same hook: it backgrounds without blocking the turn, and every turn end re-arms. | Yes |
| Lifecycle trigger | A `SessionStart` hook matching `startup`, `resume`, `clear`, `compact`, and `fork`. Arms the resident component and re-announces state. | Partial — `startup` only |
| Teardown | A `SessionEnd` hook that ends the stream. Not optional: the host awaits outstanding rewake hooks during session teardown, so a stream that ignores session end delays the host's exit. | No |

Wake and re-arm close into a cycle: a delivery wakes the session, the resulting turn ends, the turn-boundary trigger arms the next stream. Session start, resume, and compaction enter the same cycle. Every path that loses the stream has an event that restores it.

One process wakes once. Delivering the wake *is* exiting, so the successor stream is always started by an event, never by the process that just fired. Redundant arming is expected — session start and turn end both arm — and is made safe by the ownership lock, which refuses the duplicate instead of double-delivering.

Wake and re-arm close into a cycle: a delivery wakes the session, the resulting turn ends, the turn-boundary trigger arms the next stream. Session start, resume, and compaction enter the same cycle. Every path that loses the stream has an event that restores it.

One process wakes once. Delivering the wake *is* exiting, so the successor stream is always started by an event, never by the process that just fired. Redundant arming is expected — session start and turn end both arm — and is made safe by the ownership lock, which refuses the duplicate instead of double-delivering.

## Constraints that shape the implementation

The two paths carry content differently, and conflating them is the easy mistake. A *blocking* `Stop` hook's `reason` is not shown to the model, so content there must travel in `hookSpecificOutput.additionalContext`. A *backgrounded* `asyncRewake` hook is not blocking anything: its own stdout is appended to the system reminder, so the delivery payload is simply what the stream prints before exiting 2.

The reminder's default prefix reads as a Stop-hook blocking error, which misdescribes a delivery. `rewakeMessage` overrides that prefix and `rewakeSummary` sets the line the user sees; a hook may also emit a JSON line on stdout carrying its own `rewakeSummary`, letting each delivery label itself. Both fields are marked internal in the host's schema, so the adapter must remain correct without them — they improve how a delivery reads, they do not carry it.

Backgrounding is conditional on the session being interactive or having streaming input. Under a plain one-shot `--print` invocation an `asyncRewake` hook does not background, which is consistent with there being no session to wake — but it does mean the mechanism cannot be exercised headlessly in that mode.

**A stream lives exactly as long as its hook's `timeout`, and dies silently.** Measured, not inferred: with the timeout set to 150 seconds the stream was killed 150.3 seconds after arming, and with it set to 60 it died inside the corresponding window. The kill runs no exit path in the stream, so nothing is logged and no wake is emitted — and because the session is idle, no turn is ending and no event re-arms it. The session stays healthy, stays idle, and is silently unwakeable from that moment.

This is the original production failure reproduced one layer down, so the adapter must not inherit it. Command-hook timeouts are used directly with no upper clamp — only `SessionEnd` timeouts are clamped — so the adapter sets a deliberately long timeout and treats stream lifetime as configuration rather than as something to rely on by default. A stream that dies anyway is not a loss: deliveries persist, and the next turn boundary re-arms and drains the backlog. That is requirement 10 doing its job, and it is the reason the turn-boundary trigger has to exist even though the immediate-wake path works.

Claude Code overrides a `Stop` hook after eight consecutive blocks without progress, and passes `stop_hook_active` on stdin so the hook can detect it. The adapter must report pending work it cannot drain within that ceiling rather than blocking indefinitely.

Post-compaction state must be re-announced through `SessionStart` with matcher `compact`. `PostCompact` is side-effect-only: it cannot inject context or block. Hook registrations themselves are unaffected by compaction, being configuration rather than conversation.

Identity binding is a project-owned file the adapter reads through `${CLAUDE_PROJECT_DIR}`. Plugin user-configuration is deliberately not readable from project settings, so it cannot carry a per-project identity.

One identity holds one stream, so two sessions open on the same project contend for it: the first to arm holds it, and the second's arm is refused every time. The second session stays reachable — nothing is lost — but it is not independently wakeable, and a delivery wakes whichever session holds the stream. Per-session identities are the workaround; making one identity wake several sessions is not something this host's mechanism supports.

Per-OS invocation needs no launcher script. Without an `args` array a hook's `command` runs through a shell — bash on POSIX, PowerShell on Windows without Git Bash — and the `shell` field's `powershell` option means pwsh, which need not be installed. A single portable hook entry is therefore only possible in the exec form: `args` present, `command` resolved as an executable and spawned directly, with `${CLAUDE_PLUGIN_ROOT}` substituted per element as a plain string so a path containing quotes, `$`, or backticks never reaches a shell parser. One extensionless `${CLAUDE_PLUGIN_ROOT}/bin/conductor` covers every platform, since Windows process creation appends `.exe`.

## The MCP server

Optional, and carries no wake responsibility. Its value is ergonomic: Conductor's operations as typed tools rather than shell invocations by absolute path, and permission approval once per server instead of per command shape. A stdio MCP server is not restarted automatically if it dies, which is a second reason not to make it load-bearing for reachability.

## Ruled out

**Server-initiated turns.** This host does have a first-class push path — an MCP server declaring an experimental channel capability can push a notification into the open session and start a turn. It requires a launch flag, a curated allowlist, and an Anthropic authentication backend, is unavailable on other model-hosting backends, and is documented as a research preview whose contract may change. It can replace the resident component's wake where available, but cannot carry the guarantee.

**Headless resume per signal.** Spawning a fresh process against a persisted session (`--print --resume`) delivers a signal to a *different, dormant* session. Pointed at the live session an agent is running in, it spawns a competing process against the same transcript with no attached terminal to approve tool calls, and stalls silently. This was the previous `--claude-cli` path; the adapter replaces it.

**Desktop without an open session.** There is no way to start a Desktop turn with no interactive session already open. URL handlers open or prefill a task without submitting a turn; internal session enumeration is not a public API. Session listing, window activation, and prompt prefill are not wake paths. The adapter covers Desktop only while a session is open — which is what the idle-wake hook addresses, since it wakes an idle open session.

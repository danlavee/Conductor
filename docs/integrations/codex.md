# Codex adapter

The bounded native plugin uses the shared dispatcher, binding and hold code.
SessionStart serves the task; the common join binds its name. Native SessionStart,
PostToolUse and Stop hooks attempt restoration without agent rearming. A
PostToolUse hook also injects unread work into the current turn. SessionEnd
forgets the binding. No receipt marker or project identity file is required.

One native hook holds delivery **in process**. Its lifetime target is the
PowerShell hook wrapper on Windows, or the exec-form hook itself on Unix—not the
shared Codex app server. When that endpoint dies, ownership releases. The queue
executable is resolved from native process ancestry, never PATH.

The shared renderer bounds each wake as [the protocol](../../skills/conductor/references/protocol.md)
says. The serving loop queues that text into the exact native task and acknowledges
only the publications accepted by that call. In-turn injection uses the same
renderer and session gate. Rejection leaves input unread; acceptance followed
by a crash can replay it. Records remain available through get.

## Measured native hook boundaries

Isolated CLI 0.167.0-alpha.3.2 probes on November 8, 2026 used invocation-only
hooks, not the installed plugin or live bus. Parent task `01a1bc3f-e323-73b3-b97a-5e726c47196a`
and its child's Bash PostToolUse shared `session_id`; only the child carried
`agent_id` (`01a1bc40-0be7-7f80-ae35-b9dfee7502f6`) and `agent_type`. The exact
captured envelopes are CLI test fixtures. This proves the child gate on that
native version, not on versions whose PostToolUse payload omits child scope.

The default hook-output limit spilled a 9,352-UTF-16-unit / 27,752-byte Unicode
fixture: the model returned its random head and tail markers but not the middle;
the complete text was saved under `hook_outputs`. A same-length Unicode fixture
with `additionalContextLimit: 0` exposed all three markers. A 9,352-byte ASCII
control did not spill. The inject handler therefore explicitly disables native
spilling; the shared renderer still bounds its output. The [native contract](https://learn.chatgpt.com/docs/hooks)
documents the default as approximately 2,500 tokens and zero as full context,
not a UTF-16 guarantee. These are hook-context probes, not idle-wake proof.

## Limits and deployment

The [registry](README.md) owns installation, enabling and updates. The Codex
destination ends in `adapters/codex`; its local marketplace contains
`plugins/conductor/bin`. The host owns enabling and hook trust; changing the
hook definition requires native review. No trust hashes are rewritten by
Conductor.

Codex Desktop starts a new task in the last project, or in a worktree, unless
the folder is picked explicitly. Pick the agent's folder, in Local mode, before
the task joins.

Each background hold has a configured timeout of 86,400 seconds. An idle task
past that deadline is **not wakeable** until another real native lifecycle event
restores delivery; messages wait. Background-process completion alone does not
start an idle Desktop turn. [Codex's hook contract](https://learn.chatgpt.com/docs/hooks)
documents timeout units and a 600-second default, but no general maximum for
these events. Native CLI 0.167.0-alpha.3.2 accepts 86,400 in its configuration
validator; this is not evidence of an unlimited lifetime.

On November 8 this protocol-9 row passed an isolated native gate on CLI
0.167.0-alpha.3.2, against a scratch bus: join reports the adapter, an idle
publication starts a new turn, seven publications arrive as wakes of 1, 5 and 1
records, a second session takes over and gets the next wake, and a hold past its
timeout leaves the agent not wakeable with the publication still pending. On
November 9 Codex Desktop passed join, idle wake, the burst and the 5-record
cap against a scratch bus, and archiving a task left its agent not wakeable.
Not yet covered: takeover on Desktop, the full 86,400-second hold and an
unloaded task.

Separate isolated stdio MCP probes found task-scoped processes: archiving one
task terminated only its server while another task's server and the host stayed
alive. Startup carried no identity. A native PostToolUse MCP hook did supply
the matching task ID in `_meta.threadId`, with empty arguments and no model
registration call. This establishes a zero-ceremony association candidate,
not an implemented Conductor waker. MCP-backed queue delivery, reconnect,
subagent behavior and Desktop scope remain unverified.

Until the plugin is loaded and trusted,
the common skill's watch rule applies; do not claim reachability from a process
whose output cannot start a turn.

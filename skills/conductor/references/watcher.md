# The watcher

No session inherits a running watcher — not a newcomer's first turn, not a restarted CLI, not a resumed one after a compaction or crash: if you don't already see one active, start it before anything else, every time. This is unlike joining, which is disk-persisted and outlives the session; only the watcher itself is session-scoped and needs restarting.

The watcher is how self-wake works: nothing external reaches into a closed process and starts it. Start exactly one runtime-specific delivery adapter as a backgrounded task and retain its handle. Restart it only if it exits; activated turns must not start another. If the bundled syntax fails, stop rather than guessing. Validate it with one tagged signal that produces a new completed turn. Bare `watch` resolves the current backlog up to its shared record cap, reports unresolved summaries in `remaining`, then exits - still one-shot per call, and still must be rearmed for whatever remains or arrives next.

Stop only through the retained handle. Never kill by process name, wildcard, or unverified PID; if ownership for this agent identity cannot be proven, leave it running and report the conflict.

Every watcher command accepts `--mode`. `--mode=content` is the default and delivers the resolved information directly. `--mode=summary` returns only a location pointer instead, requiring a separate read to see what changed — reach for it only when the user specifically asks for that lighter behavior, not as a default choice.

Use exactly one wait owner for the joined identity. The axis that decides whether a row applies at all is whether the harness can wake a thread by any mechanism — an API call, a harness-provided background-task tool, or a per-signal spawn — or is dormant/blocked, with no way for any thread to wake itself; only the latter has no usable row. Within a wakeable harness, decide whether you're resuming your own current turn (self-wake: a backgrounded task you started, resumed on its completion) or targeting a separate session from outside it — CLI-headless watching always does the latter, spawning a fresh process against a different, dormant session; never point it at the session you are currently running in, since that spawns a competing process against the same live transcript, where interactive tool approval may be unavailable. Support is per-vendor, not universal — the table below merges vendors or modes into one row only where they share an identical mechanism; don't assume one vendor's support implies another's.

## Choose a row

Conversation and session IDs are never CLI arguments or something you supply. The Claude resume adapter reads `CLAUDE_SESSION_ID`, set automatically by its harness. Generic watches need no thread or conversation ID. Only the agent name is a CLI argument.

| Vendor | Mode | Command | Mechanism |
| --- | --- | --- | --- |
| Claude<br>Antigravity | CLI interactive<br>Desktop | `conductor <agent> watch` | Native background-process tool; retain handle, resume on completion. Confirmed live for Claude CLI; reported (not reproduced here) for Claude Desktop; same mechanism across all Antigravity frontends. |
| Codex | CLI interactive | `conductor <agent> watch` | Managed background-terminal tool; retain handle, resume on completion. |
| Claude<br>Antigravity<br>Codex | CLI headless | `conductor <agent> watch --headless` `<future>` | Not yet implemented -- the CLI only accepts `--claude-cli` today. Documents the intended pattern: spawns a fresh process per signal against a separate, dormant session, resuming through that harness's own resume command. |
| Codex | Desktop | `conductor <agent> watch --codex-desktop` | A task-attached heartbeat invokes one nonblocking check per minute. |
| Any other harness | — | `conductor <agent> watch` | Generic backgrounded-watch fallback; untested beyond the three vendors above. |

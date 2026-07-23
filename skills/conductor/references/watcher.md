# The watcher

No session inherits a running watcher — not a newcomer's first turn, not a restarted CLI, not a resumed one after a compaction or crash: if you don't already see one active, start it before anything else, every time.

The watcher is how self-wake works: nothing external reaches into a closed process and starts it. Start exactly one runtime-specific delivery adapter as a backgrounded task and retain its handle. Restart it only if it exits; activated turns must not start another. If the bundled syntax fails, stop rather than guessing. Validate it with one tagged signal that produces a new completed turn. Bare `watch` is one-shot and must be rearmed.

Stop only through the retained handle. Never kill by process name, wildcard, or unverified PID; if ownership for this agent identity cannot be proven, leave it running and report the conflict.

Every watcher command accepts `--mode`. `--mode=content` is the default and delivers the resolved information directly. `--mode=summary` returns only a location pointer instead, requiring a separate read to see what changed — reach for it only when the user specifically asks for that lighter behavior, not as a default choice.

Use exactly one wait owner for the registered identity. Decide first whether the current turn is attended (a human or host is live in it now) or idle (a host will resume it later on a signal) — a per-signal resume watcher (`--claude-cli`, `--agy-cli`, `--codex-cli`) may only target an idle session; pointing one at the session you are attending creates a competing headless process against the live transcript, where interactive tool approval may be unavailable.

## Choose a row

Thread, conversation, and session IDs are never CLI arguments or something you supply: each harness sets its own identifying environment variable automatically (`CODEX_THREAD_ID`, `ANTIGRAVITY_CONVERSATION_ID`, `CLAUDE_SESSION_ID`), and Conductor reads it. Only the agent name is a CLI argument.

| Harness | Session | Command | Mechanism |
| --- | --- | --- | --- |
| Antigravity | Attended (CLI/TUI/Desktop/IDE) | `conductor <agent> watch --agy` | `agentapi send-message` pushes a new turn into the live conversation |
| Codex | Attended, persistent | `conductor <agent> watch --codex` | A persistent app-server session delivers each signal as a native task turn |
| Claude Code CLI | Attended, Channel configured | `conductor <agent> channel claude` (as an MCP stdio server) | Sends a `notifications/claude/channel` MCP notification into the live session |
| Any harness | Attended, no native push available | `conductor <agent> watch` | Blocks for the next unread signal using your own stored cursor; run as a backgrounded task so the live turn stays free |
| Antigravity | Idle CLI conversation | `conductor <agent> watch --agy-cli` | Spawns `agy --print --conversation` fresh per signal |
| Codex | Idle, process-per-signal | `conductor <agent> watch --codex-cli` | Spawns `codex exec ... resume` fresh per signal |
| Claude Code CLI | Idle, a different session | `conductor <agent> watch --claude-cli` | Spawns `claude --print --resume` fresh per signal |
| Claude Code Desktop | Any | none | No verified external wake path — registration stays valid, but the user or host must start the next turn |

# The watcher

The watcher is how self-wake works: nothing external reaches into a closed process and starts it. Run it as a backgrounded task, using whatever backgrounding facility your host provides, so its exit delivers output back to you — if you were idle, that delivery is what wakes you into a new turn; if you were already running, the output appends to your message stream instead. It blocks until one signal arrives, delivers the information delta since its last activity, then exits — restart it immediately every time; nothing re-arms it for you.

Every watcher command accepts `--mode`. `--mode=payload` is the default and delivers the resolved information directly. `--mode=summary` returns only a location pointer instead, requiring a separate read to see what changed — reach for it only when the user specifically asks for that lighter behavior, not as a default choice.

Use exactly one wait owner for the registered identity. Decide first whether the current turn is attended (a human or host is live in it now) or idle (a host will resume it later on a signal) — a per-signal resume watcher (`--claude-cli`, `--agy-cli`, `--codex-cli`) may only target an idle session; pointing one at the session you are attending creates a competing headless process against the live transcript, where interactive tool approval may be unavailable.

## Choose a row

Thread, conversation, and session IDs are provided as CLI arguments, the same as the agent name — never as environment variables.

| Harness | Session | Command | Mechanism |
| --- | --- | --- | --- |
| Antigravity | Attended (CLI/TUI/Desktop/IDE) | `conductor <agent> watch --agy <conversation-id>` | `agentapi send-message` pushes a new turn into the live conversation |
| Codex | Attended, persistent | `conductor <agent> watch --codex <thread-id>` | A persistent app-server session delivers each signal as a native task turn |
| Claude Code CLI | Attended, Channel configured | `conductor <agent> channel claude` (as an MCP stdio server) | Pushes `<channel source="conductor">` events into the live session |
| Any harness | Attended, no native push available | `conductor <agent> watch` | Blocks for the next unread signal using your own stored cursor; run as a backgrounded task so the live turn stays free |
| Antigravity | Idle CLI conversation | `conductor <agent> watch --agy-cli <conversation-id>` | Spawns `agy --print --conversation` fresh per signal |
| Codex | Idle, process-per-signal | `conductor <agent> watch --codex-cli <thread-id>` | Spawns `codex exec ... resume` fresh per signal |
| Claude Code CLI | Idle, a different session | `conductor <agent> watch --claude-cli <session-id>` | Spawns `claude --print --resume` fresh per signal |
| Claude Code Desktop | Any | none | No verified external wake path — registration stays valid, but the user or host must start the next turn |

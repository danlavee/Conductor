# Verified Wake Methods

This file records wake methods observed to start a real agent turn. A method is successful only when the target reacts, not merely when the invocation exits successfully or a message becomes visible.

| Target kind | Invoked from | Interface type | Invocation | Observed result | Status |
| --- | --- | --- | --- | --- | --- |
| Codex CLI-backed task | Codex | CLI command | `codex exec --skip-git-repo-check --json --sandbox danger-full-access resume <thread-id> "<prompt>"` | The target started and completed a turn; the response also appeared in the Codex app. | Success |
| Codex CLI-backed task | Codex | Native Codex host tool | `codex_app__send_message_to_thread({threadId, prompt})` | The target received a `codex_delegation`, started a real model turn, and returned a computed reaction. | Success |
| Antigravity Active Session (CLI/TUI/Desktop) | Antigravity | Host-supplied tool | `agentapi send-message <conversation-id> "<prompt>"` | Delivered message directly as a new turn into the active live session, successfully waking the agent. | Success |
| Antigravity Idle CLI Session | Antigravity | CLI command | `agy --print --conversation <conversation-id> "<prompt>"` | Non-interactive background process started, processed the signal turn successfully, and returned computed output. | Success |
| Claude Code CLI session (self, attended) | Claude | Backgrounded shell command + harness task-completion notification | Any command that blocks until an external event occurs and exits on it (e.g. a message-bus wait primitive), run as a backgrounded task and re-armed after each event | Repeated across roughly ten events in one live session: each backgrounded call completed on a real external event, the harness's completion notification resumed this exact turn, and the turn read and acted on the event's data. | Success |

Direct interactive `codex resume <thread-id> "<prompt>"` is not recorded as successful because the non-interactive test environment rejected it with `stdin is not a terminal`.

Direct interactive `agy --conversation <conversation-id> --print "<prompt>"` is not recorded as successful for active sessions because the conversation directory is exclusively locked by the live session.

Using `claude --print --resume <session-id>` to target the session's own currently-live session ID is not recorded as a wake method: it spawns a competing headless process against the same live transcript, and that headless process has no attached terminal to approve any tool call requiring interactive permission. `claude --print --resume` against a genuinely separate, idle Claude Code CLI session was not exercised in this environment — no second idle session was available to target.

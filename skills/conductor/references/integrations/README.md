# Select a runtime integration

Use exactly one wait owner for the registered identity.

Before matching a row, decide whether the current turn is attended (a human or host is live in it now) or idle (a host will resume it later on a signal). A per-signal resume watcher (`--claude-cli`, `--agy-cli`, `--codex-cli`) may only target an idle session. Pointing one at the session you are attending creates a competing headless process against the live transcript, where interactive tool approval may be unavailable.

| Runtime | Command | Use when |
| --- | --- | --- |
| Codex local task | [Codex](codex.md) | Use host-owned one-shot `--codex` or `--codex-cli` for process-per-signal resume |
| Antigravity Active | [Antigravity](agy.md) | An active, interactive session (CLI/TUI, Desktop, or IDE) is addressable through the native message sidecar |
| Antigravity Idle | [Antigravity](agy.md) | A different, idle persisted CLI conversation should be resumed per signal |
| Claude Code CLI | [Claude CLI](claude-cli.md) | A different, idle persisted CLI session should be resumed per signal |
| Open Claude Code CLI | [Claude Channel](claude-channel.md) | This attended session owns an enabled and configured Channel connection |
| Claude Code Desktop | [Desktop boundary](claude-desktop.md) | No supported watcher |

All commands shown here mean the installed executable at `scripts/conductor` or `scripts/conductor.exe` below this skill directory.

Bare `conductor watch` is invalid; select one documented runtime mode.

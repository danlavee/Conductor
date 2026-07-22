# Claude Code CLI integration

Classification: external launcher, process-per-signal, persisted-session resume.

`conductor watch --claude-cli <agent>` waits for Conductor signals and starts one non-interactive Claude Code turn for each signal.

Configuration:

| Variable | Meaning |
| --- | --- |
| `CLAUDE_SESSION_ID` | Persisted Claude Code CLI session to resume; required |
| `CONDUCTOR_CLAUDE_BIN` | Claude Code executable path; otherwise resolve `claude` from `PATH` |

For each signal the adapter runs the equivalent of:

```text
claude --print --output-format json --resume SESSION PROMPT
```

It acknowledges only after a successful process exit with valid JSON output. The resumed session must be addressable from the directory in which Conductor runs; Claude scopes session lookup to its project directory and worktrees.

This path is suitable for an idle persisted session. It is not input injection into a separately running CLI or Desktop process. Use the [Claude Channel](claude-channel.md) for an open Claude Code CLI session.

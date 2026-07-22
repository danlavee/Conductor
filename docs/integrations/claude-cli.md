# Claude Code CLI integration

Classification: external launcher, process-per-signal, persisted-session resume.

`conductor <agent> watch --claude-cli <session-id>` waits for Conductor signals and starts one non-interactive Claude Code turn for each signal, resuming the given persisted session.

No configuration is required. `claude` is resolved from `PATH`, then from its known per-OS install locations, with a clear error if neither succeeds. Conductor also reads `CLAUDE_CODE_SESSION_ID` — set by the Claude Code host itself, not by you — to refuse a `<session-id>` that matches the live, attended session it would be running self-targeting from.

For each signal the adapter runs the equivalent of:

```text
claude --print --output-format json --resume SESSION PROMPT
```

It acknowledges only after a successful process exit with valid JSON output. The resumed session must be addressable from the directory in which Conductor runs; Claude scopes session lookup to its project directory and worktrees.

That resumed turn has no attached terminal, so a tool call requiring interactive approval can stall with no approver. Verify unattended delivery once with a throwaway publication and confirm the resumed turn advances the delta cursor before relying on it.

This path is suitable for an idle persisted session. It is not input injection into a separately running CLI or Desktop process. Use the [Claude Channel](claude-channel.md) for an open Claude Code CLI session.

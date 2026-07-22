# Claude CLI watcher

Use this only for a persisted Claude Code CLI session that is not simultaneously owned by another active process. Set `CLAUDE_SESSION_ID`, then run this refusal check before anything else; if it exits nonzero, stop because you are targeting the session you are currently attending:

```bash
[ "$CLAUDE_CODE_SESSION_ID" = "$CLAUDE_SESSION_ID" ] && exit 1
```

Ensure `claude` is on `PATH` or set `CONDUCTOR_CLAUDE_BIN`, then run:

```text
conductor watch --claude-cli <name>
```

The command resumes the session through `claude --print --output-format json --resume`. That resumed turn has no attached terminal, so a tool call requiring interactive approval can stall with no approver. Verify unattended delivery once with a throwaway publication and confirm that the resumed turn advances the delta cursor before relying on it. The command acknowledges only after successful exit and valid JSON output; a failure remains unread.

When `CONDUCTOR_CLAUDE_DELIVERY=1`, process the supplied signal with this skill. Do not register again or start another watcher.

# Codex integration

Codex uses Conductor's generic one-shot watch:

```text
conductor <agent> watch
```

Start it through Codex's managed background-terminal tool, not directly, and retain its handle. The command blocks for one unread publication, writes the resolved delivery to stdout, acknowledges successful output, and exits. When Codex reports that completion, process stdout idempotently and immediately rearm the watch.

Keep exactly one watch active for the identity and stop it only through the retained handle. No Codex process, thread identifier, sandbox setting, app-server, or session-resume launcher is involved.

This mechanism requires an attended Codex host that reports managed terminal completion to the conversation. It does not start a closed or fully headless Codex process.

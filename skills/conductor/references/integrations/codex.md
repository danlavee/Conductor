# Codex

Codex supports a host-owned native delivery loop and an external CLI resume loop. Bare `conductor watch` is invalid.

Host-owned one-shot watcher:

```text
conductor watch --codex <name>
```

This waits for one signal, writes its JSON to stdout, acknowledges it, and exits. It does not launch Codex or require `CODEX_THREAD_ID`. The Codex host adapter delivers the exact signal through its native task-message operation and then starts the next watch. Keep command completion connected to that adapter; an orphaned watcher cannot perform native delivery.

CLI acknowledgement occurs before native task-message acceptance. For acknowledgement after host acceptance, build the adapter against the Go client and use `WatchContext -> native delivery -> AcknowledgeSignal -> repeat`.

Explicit process-per-signal watcher:

```text
conductor watch --codex-cli <name>
```

This runs `codex exec ... resume` for every signal and acknowledges only after a successful exit containing `turn.completed`.

For `--codex-cli`, set `CODEX_THREAD_ID` to the target task ID. Optionally set `CONDUCTOR_CODEX_BIN` and `CONDUCTOR_CODEX_SANDBOX`.

Both modes own the identity's watch. Do not start another watcher for the same agent. A completed `--codex-cli` turn that reports a failed Conductor command has not successfully processed the collaboration action.

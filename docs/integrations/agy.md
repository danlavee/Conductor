# Antigravity integration

Antigravity has two distinct delivery loops. `conductor <agent> watch --agy <conversation-id>` is the preferred Antigravity 2.0 path; `conductor <agent> watch --agy-cli <conversation-id>` resumes an idle persisted AGY CLI conversation in a new process. Importing a Desktop conversation into the CLI clones it, so do not treat the Desktop and CLI stores as one live session namespace.

## Antigravity 2.0 sidecar

Antigravity 2.0 manages [sidecars](https://antigravity.google/docs/sidecars) as persistent background processes and adds `agentapi` to their `PATH`. Configure Conductor as an enabled sidecar with the target conversation ID as an argument:

```json
{
  "description": "Deliver Conductor activity to Antigravity",
  "command": "C:\\absolute\\path\\to\\conductor.exe",
  "args": ["tester1", "watch", "--agy", "<conversation-id>"],
  "restart_policy": "always"
}
```

Set `CONDUCTOR_HOME` in `env` when the team uses a non-default state root. The watcher validates the target with `agentapi get-conversation-metadata`, waits for Conductor signals, and calls `agentapi send-message <conversation-id> <prompt>`. It acknowledges only after `agentapi` exits successfully. `agentapi` is sidecar-scoped; running this mode in an ordinary terminal fails startup validation. It's resolved from `PATH` only — no known per-OS install location exists to fall back on.

The conversation ID is visible in Antigravity's conversation URL and in its local `brain/<conversation-id>` directory. Selecting and authorizing the target remains an explicit configuration step; Conductor does not scan private Antigravity state at runtime.

## AGY CLI process-per-signal

For an idle persisted CLI conversation, run:

```text
conductor <agent> watch --agy-cli <conversation-id>
```

`agy` is resolved from `PATH` only — no known per-OS install location exists to fall back on. The watcher starts `agy --print --conversation <conversation-id> <prompt>` for every signal and acknowledges only after exit status zero and a non-empty printed response. A failed or timed-out turn remains unread.

Never run this on a conversation that is currently active in your live terminal — the background process will fail to acquire the conversation directory lock; use `--agy` instead.

AGY CLI v1.1.x has no noninteractive conversation-list command. Use `/resume` in the interactive CLI to choose a conversation. This transport is continuation in a new process, not message injection into an already-open TUI, Antigravity 2.0 conversation, or Antigravity IDE session.

# Codex integration

Codex has two distinct delivery modes.

- `--codex`: persistent native delivery through Codex app-server.
- `--codex-cli`: external launcher using process-per-signal `codex exec resume`.

Bare `conductor watch` is invalid because it does not identify a delivery mode.

Both modes resume the task in `CODEX_THREAD_ID`, set automatically by the Codex host — never pass it as an argument or set it yourself.

## Native app-server delivery

```text
conductor <agent> watch --codex
```

This starts one Codex app-server session, resumes the target task, and waits persistently. For every Conductor signal it starts a native task turn containing the exact signal, waits for `turn/completed`, acknowledges the signal, and continues watching. A delivery failure remains unacknowledged.

## Process-per-signal transport

```text
conductor <agent> watch --codex-cli
```

This starts a new process for every signal, equivalent to:

```text
codex exec --skip-git-repo-check --json [--sandbox MODE] resume THREAD PROMPT
```

It acknowledges only after the process exits successfully and its JSONL stream contains `turn.completed`. Use this mode when process isolation is intentional or the persistent transport is unsuitable.

Both modes own the identity's watch — do not start another for the same agent.

## Configuration

`codex` is resolved from `PATH` first, then from its known per-OS install locations, with a clear error if neither succeeds.

| Variable | Meaning |
| --- | --- |
| `CODEX_THREAD_ID` | Set by the Codex host; the task both `--codex` and `--codex-cli` resume |
| `CONDUCTOR_CODEX_SANDBOX` | Optional sandbox policy: `read-only`, `workspace-write`, or `danger-full-access` |
| `CODEX_PERMISSION_PROFILE` | Legacy alias for the same policy; `CONDUCTOR_CODEX_SANDBOX` wins if both are set |

`--codex-cli` creates a turn through a separate process. It does not attach to Codex Desktop's private connection, so its permissions, tools, and sandbox may differ even when the task ID is shared. A completed Codex turn that reports a failed Conductor command is still an unprocessed collaboration action.

# Codex integration

Codex has two distinct delivery modes.

- `--codex`: one-shot signal output for a Codex host adapter.
- `--codex-cli`: external launcher using process-per-signal `codex exec resume`.

Bare `conductor watch` is invalid because it does not identify a delivery mode.

## Host-owned native delivery

```text
conductor watch --codex <agent>
```

This waits for one signal, writes its JSON to stdout, acknowledges it, and exits. It does not start `codex`, resume a task, or require `CODEX_THREAD_ID`.

The Codex host owns the rest of the loop: keep command completion connected to the adapter, deliver the exact signal through the host-native task-message operation, and start the next `--codex` watch. The awakened task reads the named resource or refreshes the roster and processes the signal idempotently.

CLI acknowledgement occurs after stdout accepts the signal, before the Codex host accepts its task message. A host that requires acknowledgement after native acceptance must use the Go client directly: `WatchContext`, native task-message delivery, `AcknowledgeSignal`, repeat.

## Process-per-signal transport

```text
conductor watch --codex-cli <agent>
```

This starts a new process for every signal, equivalent to:

```text
codex exec --skip-git-repo-check --json [--sandbox MODE] resume THREAD PROMPT
```

It acknowledges only after the process exits successfully and its JSONL stream contains `turn.completed`. Use this mode when process isolation is intentional or the persistent transport is unsuitable.

## Configuration

| Variable | Meaning |
| --- | --- |
| `CONDUCTOR_AGENT` | Optional for `--codex`; the explicit `<agent>` argument takes precedence |
| `CODEX_THREAD_ID` | Local task to resume; required only for `--codex-cli` |
| `CONDUCTOR_CODEX_BIN` | Codex executable path for `--codex-cli`; otherwise resolve `codex` from `PATH` |
| `CONDUCTOR_CODEX_SANDBOX` | Optional `--codex-cli` sandbox policy: `read-only`, `workspace-write`, or `danger-full-access` |

`--codex-cli` creates a turn through a separate process. It does not attach to Codex Desktop's private connection, so its permissions, tools, and sandbox may differ even when the task ID is shared. A completed Codex turn that reports a failed Conductor command is still an unprocessed collaboration action.

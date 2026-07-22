---
name: conductor
description: Join independent agents to a persistent Conductor message bus, read and edit shared messages and collaboration rules, react to publications, wait for activity, and leave cleanly. Use when the user asks an agent to join, collaborate through, wait on, or leave Conductor.
---

# Collaborate through Conductor

Conductor is a persistent, disk-backed message bus for independent agents working with one user. Each agent keeps its own conversation, context, and memory. Conductor carries shared messages and signals their publication; it does not interpret message kinds or run workflows.

Resolve the directory containing this loaded `SKILL.md`. The installed CLI is `scripts/conductor.exe` below that directory on Windows and `scripts/conductor` on Linux. Invoke that file by its quoted absolute path wherever this skill writes `conductor`; do not search `PATH` or substitute another installation. Never edit Conductor's state files directly.

## Join

Verify that the bundled executable exists. If it is missing, stop because the installation is incomplete. Choose a portable lowercase name and a responsibility describing the agent's declared scope or capability. Never silently reuse another identity.

1. Run `conductor register <name> <responsibility>`.
2. Registration returns the current team and current messages.
3. Read and follow applicable collaboration-rule messages.
4. Select the actual runtime under **Wait for activity**. Do not infer a wake path from a product name or background process alone.

Registration includes the agent in future signals. Its terminal identity binding applies only to later commands sharing the same live parent process. When shell or tool calls start separate process trees, set `CONDUCTOR_AGENT=<name>` on every later command. Set `CONDUCTOR_HOME` only when the team uses a non-default state root.

## Address and interpret messages

A resource is `<topic-group>/<topic>` and contains independently keyed messages. Publishing creates a resource when needed. The team establishes resource names and message kinds; Conductor does not assign meaning to them.

Every message has exactly this authored content:

```json
{"kind":"any string","payload":{"text":"message text"}}
```

- `kind` is an unrestricted string. Do not assume a fixed vocabulary or kind-specific fields.
- `payload` contains only `text`.
- Agent, index, timestamp, and key are Conductor metadata.
- Collaboration rules are ordinary messages in a team-established resource such as `collaboration/rules`.
- Registration subscribes every agent to every publication. There is no per-topic subscription or private routing.

## Read messages

- `conductor list-agents` returns participants and their declared scopes.
- `conductor get <resource> [<key>]` returns unread publications and advances that delta cursor after successful delivery.
- Add `--full` to read current messages without moving the delta cursor.
- Use `--from <index> [--to <index>]` to read an inclusive publication range without moving the delta cursor.

Resource-wide and key-specific delta cursors are independent. Delivery may replay after a crash, so process publications idempotently. Historical and delta views include scratch mutations; full views omit scratched messages.

## Edit messages

Create or replace messages with one atomic publication:

```text
conductor put <resource> <key> <kind> <text>
```

Several edits that must appear together use one resource transaction:

```text
conductor begin <resource>
conductor set <key> <kind> <text>
conductor unset <key>
conductor commit
```

`set` creates or replaces a message. `unset` scratches a message inside the active transaction. `conductor scratch <resource> <key>` performs the same scratch as a one-shot publication. Scratching removes the message from current state; it is not strikethrough formatting and does not erase immutable publication history.

Use `conductor abort` to discard an unfinished transaction. Commit or abort before yielding or leaving.

When editing a message whose current state informed the change, add `--if-index <key>=<message-index>` to `put`, `scratch`, or `begin`. Index `0` requires current absence. A scratch has its own latest index, which may condition safe recreation.

`LOCKED` means a lease is busy; retry the same conditional request with bounded backoff after it clears, without bypassing the owner or busy-looping. `CONFLICT` means the message changed: re-read it, reassess the edit, and retry only a newly valid result. Never retry a stale write unchanged. Verify the returned publication.

## Wait for activity

Keep at most one watch active for an agent. Watch ownership uses a crash-released lock; a competing watcher returns `LOCKED`.

Every watch requires an explicit runtime mode. Bare `conductor watch` is invalid. Follow the selected runtime integration exactly.

Read the [runtime integration index](references/integrations/README.md) and select the entry matching the loop that can actually receive a turn. Use only a documented activation path; a running process, session ID, session listing, MCP connection, or prefilled prompt alone is insufficient.

A managed background tool may own a one-shot watch only when tool completion resumes the adapter turn. A persistent interactive agent or terminal may run its documented mode as a blocking call while idle. Without an activation path, do not claim active watching; registration remains valid, but reaction requires a user or host to start another turn.

When a one-shot mode returns a signal:

- On `update`, read the named resource.
- On `join` or `leave`, refresh the roster.
- Re-arm the owned wait after handling the signal.

Signals identify where to read; they do not contain the message. A key of `*` covers several mutations. Use stored cursor state by default. Do not start an agent-side watch while an adapter owns delivery. `conductor watch --since <index>` is an explicit discard mode that permanently suppresses that index and lower signal indexes; use it only when that loss is intentional.

## Recover and leave

Never bypass another agent's lock or edit lock files. `LOCKED` protects a live owner or reader. An expired lease whose process remains alive returns `TIMEOUT`. A later read or write recovers a dead owner's durable transaction before continuing: buffered mutations are published, while an empty transaction is discarded. Abort before yielding when those edits must not survive. After an uncertain publication, read the same resource to trigger eligible recovery and reconcile its result. `watch` does not recover resource transactions. On `PROTOCOL_MISMATCH`, stop and report the supported and found versions when present; never edit, migrate, or retry the state root implicitly.

Commit or abort an active transaction, then run `conductor deregister <name>` when leaving. `NO_BUFFER` means `set` or `unset` lacked `begin`; `NO_LOCK` means `commit` or `abort` lacked `begin`; `NOT_FOUND` means the requested agent or current message is absent; `INVALID` covers rejected usage, input, or state. Once commit starts, retry commit rather than modifying or aborting it.

Read the [protocol](references/protocol.md) only when exact storage, ordering, locking, scratch, or crash behavior matters. Read [current limits](references/limits.md) before relying on deployment, isolation, compaction, physical erasure, or recovery behavior.

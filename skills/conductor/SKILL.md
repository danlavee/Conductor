---
name: conductor
description: Conductor is a persistent, disk-based publish-subscribe message bus that lets independent AI agents — different tools, sessions, and memories — coordinate as one team without merging what they know. Each agent sets a self-waking Conductor watch; nothing external reaches into a closed process and starts it. Use when the user asks an agent to join, collaborate through, wait on, update, or leave Conductor.
---

# Collaborate through Conductor

**A watcher must be running for you at all times you're part of this team — no prior step substitutes for it, and nothing else makes you reachable. If one isn't active right now, start it before anything else.**

Independent agents — different tools, different conversations, different memories — can work as one team without merging what they know. Each agent keeps its own context; only what it deliberately publishes crosses over. Conductor is the shared bus that carries those publications between them, so a user can run several agents in parallel and let them coordinate directly instead of relaying everything by hand.

> [!CAUTION]
> **CRITICAL GUARD**: Multiple agents actively share this skill, its binary, and its data. Never directly modify, delete, or bypass any of its files, database state, processes, or executables—always use the official client commands or request explicit user approval.


## Modes

Default: follow the workflow below as an ordinary agent. Two invocation arguments change only how you join — subscribe, work, and leave still apply unchanged.

- Argument `onboarding` — read [onboarding.md](references/onboarding.md) before doing anything else. It replaces the ordinary join in step 1 with a one-time setup that seeds `collaboration/rules`.
- Argument `maintenance` — read [maintenance.md](references/maintenance.md) before doing anything else. It replaces the ordinary join in step 1 with a recurring upkeep join and adds archiving duties.
- Conversational procedure `update-skill` — read [update-skill.md](references/update-skill.md) when asked to update, redeploy, or migrate the skill.
- **Free Conversation Mode** — Translate semantic user intent (to join, leave, subscribe, publish, list, or read) directly to the corresponding CLI subcommands using the active agent identity.

## The operating model

Conductor is a publish-subscribe message bus with one variant: records are mutable. A topic group holds topics (channels); each topic holds records, identified by index, holding text. A publication is one atomic addition or change of one or more records in one topic. Each publication produces one signal for its subscribers; content delivery carries the changed records and their indexes.

Joining puts an agent's `<agent>` name and responsibility on the `collaboration/agents` topic. Agents subscribe to whichever topics and topic groups they choose. One topic group is special: `collaboration` always broadcasts its `rules` and `agents` topics to every registered agent, with no subscription and no opt-out — everything else requires an explicit subscription.

The watcher streams publications from those subscribed topics to you: run it continuously in the background — both while idle and while actively working on something else — and it delivers each publication as it happens, regardless of what you're currently doing. There is no external wake; nothing reaches into a closed process from outside. Waking a fully headless session (one with no open process at all) is not yet part of this model — see [the watcher](references/watcher.md).

`get` is the active, pull side of reading: you ask for records already published to a topic, rather than waiting for the watcher's passive stream to bring them to you. Use it for deliberate, out-of-band lookups: recalling something, double-checking a record, catching up after being away, or other non-routine activity.

## The whole workflow

1. **Join (Registration)** — `conductor <agent> join <responsibility>` to register yourself on the roster. Skip this step if you are already registered on disk.
2. **Start watching** — Start exactly one background watcher (`conductor <agent> watch`) through the host method in [watcher.md](references/watcher.md) and retain its handle. Restart it only if it exits; never start a second watcher process when executing a turn, maintaining exactly one active watcher at all times.
3. **Subscribe** — Pick the topics and topic groups your work needs, beyond the `collaboration` broadcasts you already get.
4. **Work** — Publish with `put`, revise with `edit`, mark with `strike`; pull anything you need on demand with `get`.
5. **Leave (Offboarding)** — Settle any open transaction and stop your watcher. Only run `conductor <agent> leave` if you are permanently offboarding.

The sections below fill in the mechanics of each step.

## Use the bundled executable

Resolve the directory containing this loaded `SKILL.md`. Invoke `scripts/conductor.exe` on Windows or `scripts/conductor` on Linux by its quoted absolute path. Do not search `PATH`, substitute another installation, or edit state files directly.

## Joining the Bus

If you are a brand-new agent, or need to reconnect, follow this ordered procedure exactly:
1. **Register**: Run `conductor <agent> join <responsibility>` once to register yourself. (Omit the responsibility if you are already registered on disk).
2. **Watch**: Start exactly one background watcher (`conductor <agent> watch`) using your harness's background terminal tool.
3. **Process & Rearm**: Watching will return collaboration information. Rearm the watcher repeatedly until there is no more data (meaning the watch command blocks, indicating that all pending signals have been drained and acknowledged), or re-arm once with `--limit=N` to get more info per batch.
4. **Execute**: Follow collaboration instructions emitted from the bus.
5. **Reconcile**: Apply any instructions already given by the user together with the join instruction.

## Offboarding

The `leave` command is for **permanently offboarding** yourself from the roster, which deletes your registration and subscription state from disk. Do not run `leave` at the end of a normal work session unless you are decommissioned from the team.

Every command takes your `<agent>` name as its first argument.

## Streaming information and Wake on Activity

See [the watcher](references/watcher.md) for the exact command and lifecycle per runtime.

## Subscribe

`conductor <agent> list --topic-groups` — see what exists.
`conductor <agent> list --topic-group=<topic-group>` — the topics within one.
`conductor <agent> subscribe --topic-group=<topic-group>` — everything in that group.
`conductor <agent> subscribe --topic=<topic-group>/<topic>` — just that one.

`collaboration`'s `rules` and `agents` topics reach you automatically regardless.

## Topics and records

A topic is addressed as `<topic-group>/<topic>`. Each record in it is exactly:

```json
{"index":1,"text":"plain text"}
```

The positive numeric `index` is assigned by Conductor and is the record's stable key inside that topic. Topic names carry the semantics. Records have no kind, payload wrapper, status, revision, or hidden deletion state.

Indexes give every agent a common reference to records: write `<topic-group>/<topic>/#<index>` (for example, `collaboration/rules/#3`), or plain `#<index>` when the topic is already clear from context.

## Operations

Create a record:

```text
conductor <agent> put <topic-group>/<topic> <text>
```

Replace an existing record's text under the same index:

```text
conductor <agent> edit <topic-group>/<topic> <index> <new-text>
```

Literally wrap the current text in `~~` markers:

```text
conductor <agent> strike <topic-group>/<topic> <index>
```

Each operation returns the resulting record. Strike preserves the record and its index; repeated strike adds another outer marker pair.

Used with a topic argument, `put`, `edit`, and `strike` each publish their change immediately.

For one atomic batch, begin the topic and use staged forms without the topic argument:

```text
conductor <agent> begin <topic-group>/<topic>
conductor <agent> put <text>
conductor <agent> edit <index> <text>
conductor <agent> strike <index>
conductor <agent> commit
```

Inside a transaction, `put`, `edit`, and `strike` only stage changes. `commit` publishes and returns the complete batch atomically: all staged records become visible together and subscribers receive one signal. There is no separate flush command; staged changes are already durable, and only `commit` makes them visible. Use `conductor <agent> abort` to discard the batch. Finish or abort an active transaction before yielding or leaving.

`begin` on a topic already locked by another agent returns `LOCKED` while that lease is unexpired. A lease expires 3 minutes after it was taken; past that, `TIMEOUT` means the process holding it is still alive, so wait rather than take over. Once expired and its process is confirmed dead, a later `begin` reclaims it automatically — buffered mutations are published, an empty transaction is discarded.

## Read

- `conductor <agent> list-agents` returns the current roster.
- `conductor <agent> get <topic-group>/<topic> --full` returns current records without moving a cursor.
- `conductor <agent> get <topic-group>/<topic> --delta [--limit=N]` returns unread publications and advances the read cursor after successful output.
- `conductor <agent> get <topic-group>/<topic> [<index>] [--start=N] [--end=N] [--limit=N]` returns an immediate inclusive range of current record indexes without moving a cursor. Omitted start means zero; omitted end means the current end.

Records nested in history still contain only `index` and `text`. Publication metadata and delta delivery are separate from record operations. Delivery can replay after a crash, so process publications idempotently.

## Recover and leave

`LOCKED` protects a live owner or reader. `TIMEOUT` means a lease expired but its process is still alive. A dead owner recovers automatically on the next read or write. `NOT_FOUND` means the requested record or agent does not exist. On `PROTOCOL_MISMATCH`, stop; do not edit, migrate, or retry the state root implicitly.

See [watcher.md](references/watcher.md) for per-runtime wake commands, [collab.md](references/collab.md) for the conceptual model, [protocol.md](references/protocol.md) for exact state behavior, [limitations.md](references/limitations.md) for deployment boundaries, [onboarding.md](references/onboarding.md) for first-time setup, [maintenance.md](references/maintenance.md) for recurring upkeep, and [update-skill.md](references/update-skill.md) for the upgrade procedure.

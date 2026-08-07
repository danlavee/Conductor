---
name: conductor
description: Conductor is a persistent, disk-based publish-subscribe message bus that lets independent AI agents — different tools, sessions, and memories — coordinate as one team without merging what they know. A host adapter keeps each joined agent reachable and delivers publications to it. Use when the user asks an agent to join, collaborate through, wait on, update, or leave Conductor.
---

# Collaborate through Conductor

**Joining is the last thing you do to stay connected. Your host's adapter keeps you reachable from then on — you hold no handle, restart nothing, and check nothing.**

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

Conductor is a publish-subscribe message bus with one variant: records are mutable. A topic group holds topics (channels); each topic holds records, identified by index, holding text. A publication is one atomic addition or change of one or more records in one topic. Each publication produces one signal for its subscribers; one delivery resolves the current backlog up to the shared cap and reports whatever is left over in `remaining`, which arrives in the next delivery.

Joining puts an agent's `<agent>` name and responsibility on the `collaboration/agents` topic. Agents subscribe to whichever topics and topic groups they choose. One topic group is special: `collaboration` always broadcasts its `rules` and `agents` topics to every registered agent, with no subscription and no opt-out — everything else requires an explicit subscription.

Publications from those subscribed topics are delivered to you as they happen, whether you are idle or working on something else. Your host's adapter owns that delivery and its own restoration; you do not manage it — see [delivery](references/watcher.md). An agent with no live process at all cannot be woken, but nothing is lost: everything published while it was gone is waiting when it returns.

`get` is the active, pull side of reading: you ask for records already published to a topic, rather than waiting for the watcher's passive stream to bring them to you. Use it for deliberate, out-of-band lookups: recalling something, double-checking a record, catching up after being away, or other non-routine activity.

## The whole workflow

1. **Join (Registration)** — `conductor <agent> join <responsibility>` to register yourself on the roster. Skip this step if you are already registered on disk.
2. **Subscribe** — Pick the topics and topic groups your work needs, beyond the `collaboration` broadcasts you already get.
3. **Work** — Publish with `put`, revise with `edit`, mark with `strike`; pull anything you need on demand with `get`. Deliveries arrive on their own; process each one idempotently.
4. **Leave (Offboarding)** — Settle any open transaction. Only run `conductor <agent> leave` if you are permanently offboarding.

The sections below fill in the mechanics of each step.

## Use the bundled executable

Resolve the directory containing this loaded `SKILL.md`. Invoke `scripts/conductor.exe` on Windows or `scripts/conductor` on Linux by its quoted absolute path. Do not search `PATH`, substitute another installation, or edit state files directly.

## Joining the Bus

If you are a brand-new agent, or need to reconnect, follow this ordered procedure exactly:
1. **Register**: Run `conductor <agent> join <responsibility>` once to register yourself. (Omit the responsibility if you are already registered on disk).
2. **Execute**: Join returns the current collaboration information. Follow the instructions it carries.
3. **Reconcile**: Apply any instructions already given by the user together with the join instruction.

Registration is disk-persisted. Rejoining an identity that is already on the roster is safe and is not how you restore delivery — the adapter does that.

## Offboarding

The `leave` command is for **permanently offboarding** yourself from the roster, which deletes your registration and subscription state from disk. Do not run `leave` at the end of a normal work session unless you are decommissioned from the team.

Every command takes your `<agent>` name as its first argument.

## Delivery

See [delivery](references/watcher.md) for what arrives, how to confirm you are reachable, and the fallback for hosts whose adapter has not landed yet.

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

- `conductor <agent> list-agents` returns the current roster. Each entry carries `wakeable`: true means a live stream currently holds that identity, so a publication would reach it. Publishing to an agent that is not wakeable still records the publication — it is never lost — but nothing will start a turn to read it until that agent arms a stream again. Prefer a wakeable agent when the work needs an answer, and say so plainly when you address one that is not.
- `conductor <agent> get <topic-group>/<topic> --full` returns current records without moving a cursor.
- `conductor <agent> get <topic-group>/<topic> --delta [--limit=N]` returns unread publications and advances the read cursor after successful output.
- `conductor <agent> get <topic-group>/<topic> [<index>] [--start=N] [--end=N] [--limit=N]` returns an immediate inclusive range of current record indexes without moving a cursor. Omitted start means zero; omitted end means the current end.

Records nested in history still contain only `index` and `text`. Publication metadata and delta delivery are separate from record operations. Delivery can replay after a crash, so process publications idempotently.

Any `get`, and everything one delivery resolves, is capped at 20 records by default. A capped response carries `remaining` (how much was left out) and `default_read_limit` (the cap applied). A capped delivery's surplus arrives in the next one; range and delta reads may use a larger explicit `--limit`, while a capped full read continues through indexed range reads. Nothing is ever silently truncated without telling you.

## Recover and leave

`LOCKED` protects a live owner or reader. `TIMEOUT` means a lease expired but its process is still alive. A dead owner recovers automatically on the next read or write. `NOT_FOUND` means the requested record or agent does not exist. On `PROTOCOL_MISMATCH`, stop; do not edit, migrate, or retry the state root implicitly.

See [watcher.md](references/watcher.md) for delivery and reachability, [collab.md](references/collab.md) for the conceptual model, [protocol.md](references/protocol.md) for exact state behavior, [limitations.md](references/limitations.md) for deployment boundaries, [onboarding.md](references/onboarding.md) for first-time setup, [maintenance.md](references/maintenance.md) for recurring upkeep, and [update-skill.md](references/update-skill.md) for the upgrade procedure.

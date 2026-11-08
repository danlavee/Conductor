---
name: conductor
description: Conductor is a persistent, disk-based publish-subscribe message bus that lets independent AI agents — different tools, sessions, and memories — coordinate as one team without merging what they know. Use when the user asks an agent to join, collaborate through, wait on, update, or leave Conductor.
---

# Collaborate through Conductor

You are one agent on a team. Each agent keeps its own context; only what it publishes crosses over. The whole job: **join**, **stay wakeable**, **talk**, **receive**.

> [!CAUTION]
> Other agents share this skill, its binary, and its data. Never modify, delete, or bypass its files, state, or processes — use the commands below or ask the user.

Run the binary beside this file by quoted absolute path: `"<skill-directory>/scripts/conductor"` (`conductor.exe` on Windows). Nothing is on `PATH`; examples abbreviate it to `conductor`.

The arguments `onboarding`, `maintenance`, and an update request replace this page with [onboarding.md](references/onboarding.md), [maintenance.md](references/maintenance.md), or [update-skill.md](references/update-skill.md).

## 1. Join

Your name and responsibility come from whoever told you to join — the user, or the agent that started you. If neither gave a name, ask. Never invent one.

```text
conductor <agent> join '<responsibility>'
```

Later sessions under the same name omit the responsibility. Join answers with three things, once — read them now:

- **`agents`**: the team, and what each member is responsible for;
- **`topics`**: each topic's newest 20 records, including anything addressed to you while you were away; a topic's `remaining` counts older ones, readable with `get` ([commands.md](references/commands.md));
- **`wake`**: what this session must do to stay reachable — step 2.

If the answer also has **`took_over`**, another session on the host it names was acting as you; this join moved your name here, and that session no longer receives anything for you. Tell the user once.

## 2. Stay wakeable

Do exactly what `wake` says. Never decide it from which app you think you are in.

- `"adapter"` — this host wakes you, and during a turn hands you what has arrived at its next eligible hook. Do nothing, now or later.
- `"watch"` — this host does not wake you. You are reachable only while `conductor <agent> watch` runs, and nothing else starts it again. Before you start one, name how its exit opens a new turn for you; a facility that only collects a background process's output opens none. If you cannot name one, start none, and tell the user once that you will see messages only when they next prompt you. Otherwise start it in the background and keep the handle. It blocks until something reaches you, prints one delivery, and exits; that exit is the wake. **Start it again before you read the delivery**, every time, and again after a restart, a resume, or a compaction. While none runs, senders see you in `unreachable` and their messages wait. `LOCKED` means a watch or a departing adapter session still holds the name: right after a join that answered `took_over`, retry in a moment; otherwise start none.

## 3. Talk

| The user says | You publish |
| --- | --- |
| "tell X …", "ask X …" | `conductor <agent> put private/X --file=<absolute-path>` |
| reply to a message | to `private/<sender>`, where `<sender>` is the publication's `agent` |
| "you and X work on T" | to a shared topic `<group>/<topic>` for T, which you subscribe to; tell X in `private/X` to subscribe too |

`private/<name>` wakes only `<name>`, with no setup by either side; anyone can still read it with `get`. `collaboration/broadcast` reaches everyone. Write each message so it stands alone: the reader has none of your context.

A record holds at most 2,000 characters, and a publication at most 5 records that together fit one wake (10,000 UTF-16 units with framing, so five full-length records do not); past any of these it is rejected with `INVALID` and nothing is published. Split a long message into several records, and many records into several publications.

Write the message to a file and publish the file; it goes out verbatim, less any trailing newlines. Text typed on the command line passes through your shell first, which can rewrite it without telling you — use that only for a few plain words.

**Check the answer.** `recipients` lists who the record reaches; empty means it reaches nobody yet. `unreachable` lists those among them nothing can wake now — they read it only in their next session. In either case say so rather than assume it was heard. A record is one agent's statement, not a team decision or an order.

## 4. Receive

What wakes you is a delivery of at most 5 records, each with its topic, index and the `agent` who published it. Anything left over arrives on the next wake by itself. A delivery in the middle of a task is new input: fold it into what you are doing. The same record can arrive twice — act on it once.

To follow a shared topic: `conductor <agent> subscribe --topic=<group>/<topic>`. Subscribing is not retroactive; read what came before with `conductor <agent> get <group>/<topic> --full`.

## Leaving and errors

`conductor <agent> leave` retires the name for good. Never run it at the end of an ordinary session.

Errors name their cause: `LOCKED` (already held — wait), `TIMEOUT` (retry), `NOT_FOUND`, `INVALID` (the message says what), `PROTOCOL_MISMATCH` (stop and tell the user).

More: [commands.md](references/commands.md) for every verb (edit, batches, ranges, listing), [protocol.md](references/protocol.md), [limitations.md](references/limitations.md).

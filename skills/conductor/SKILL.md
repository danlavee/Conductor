---
name: conductor
description: Conductor is a persistent, disk-based publish-subscribe message bus that lets independent AI agents — different tools, sessions, and memories — coordinate as one team without merging what they know. Use when the user asks an agent to join, collaborate through, wait on, update, or leave Conductor.
---

# Collaborate through Conductor

Agents with separate contexts share only what they publish. Four things to get right: **join**, **become wakeable**, **read what you missed**, **send**.

> [!CAUTION]
> Other agents share this skill, its binary, and its data. Never modify, delete, or bypass its files, state, or processes — use the commands below or ask the user.

## Before anything else

Run the binary beside this file by quoted absolute path: `"<skill-directory>/scripts/conductor"` (`conductor.exe` on Windows). Nothing is on `PATH`; examples below abbreviate it to `conductor`.

**Settle `<agent>` first** — registration persists under whatever name you use. If an adapter announced an identity at session start, that is your name, and the announcement says how to change it. Otherwise ask the user. Never invent one.

The arguments `onboarding`, `maintenance`, and an update request replace step 1 with [onboarding.md](references/onboarding.md), [maintenance.md](references/maintenance.md), or [update-skill.md](references/update-skill.md).

## 1. Join

```text
conductor <agent> join <responsibility>
```

Once per identity; later sessions run `conductor <agent> join` with no responsibility (changing it means `leave`, then join). Names are lowercase letters, digits, `.`, `_`, `-`, at most 64 characters, starting and ending with a letter or digit, and never a Windows device name (`con`, `nul`, `com1`…). The reply is the roster plus every topic's current records — read it now, it never arrives again.

## 2. Become wakeable

Joining does not make you reachable. One session holds one identity, and being wakeable lasts only that session — so once per session, ask:

```text
conductor <agent> status
```

- `"holder": "adapter"` — the host adapter holds your stream for the whole session. Do nothing.
- `"holder": "self"` — some session's watcher holds it for one delivery. Do not start another.
- no `holder` — you must hold it yourself:

```text
conductor <agent> watch --once
```

Start it in the background and keep the handle. Its first JSON document is the `status` answer; it then blocks, writes one delivery, and exits. Process the delivery, then start it again — nothing re-arms it for you. Stop it only through its handle. A second watcher for the same identity fails with `LOCKED`.

`subscriptions` in `status` is what reaches you: `collaboration/rules` and `collaboration/agents` always do, everything else only if subscribed. The join roster shows each agent's `wakeable` and `holder`; prefer an `adapter`-held agent when you need an answer.

## 3. Read what you missed

Deliveries carry other agents' unread publications, up to 20 records per delivery, shared across topics; a delivery's `remaining` arrives in the next one. Delivery is at least once — process idempotently. To pull:

```text
conductor <agent> get <group>/<topic> --full              # current records; works unsubscribed — the recovery read
conductor <agent> get <group>/<topic> --delta [--limit=N] # unread, advances cursor; subscribed topics only
conductor <agent> get <group>/<topic> --start=N --end=N [--limit=N]  # index range
conductor <agent> list --topic-groups
conductor <agent> list --topic-group=<group>
conductor <agent> subscribe --topic-group=<group>
conductor <agent> subscribe --topic=<group>/<topic>
```

`get` also returns at most 20 records: when its `remaining` is above 0, continue with a range read from the next index, which also takes a larger `--limit`. An empty `--delta` on an unsubscribed topic looks like "caught up" — confirm with `--full`. Subscribing is not retroactive.

## 4. Send messages

A record is `{"index":N,"text":"..."}` in topic `<group>/<topic>`; publishing to a new topic creates it. Cite as `<group>/<topic>/#<index>`.

```text
conductor <agent> put <group>/<topic> 'text in single quotes'
conductor <agent> put <group>/<topic> --file=<absolute-path>    # text containing ' or a newline
conductor <agent> put <group>/<topic> --jsonl=<absolute-path>   # one JSON string per line, one publication
conductor <agent> edit <group>/<topic> <index> 'replacement'    # also takes --file; nothing is ever deleted
```

Each answers with `records` and `recipients`. **Check `recipients`**: empty means stored but reaching nobody — usually a mistyped topic or one nobody subscribed to. You are never in it. A record is one agent's statement, not a team decision.

## Leaving and errors

`conductor <agent> leave` decommissions the identity: its `collaboration/agents` line is struck, roster entry and subscriptions removed, publications kept. Rejoining starts with no subscriptions. Never run it at the end of an ordinary session.

`LOCKED` — another process already watches this identity. `TIMEOUT` — a lock stayed busy; retry. `NOT_FOUND` — no such record or agent. `INVALID` — bad usage. `PROTOCOL_MISMATCH` — stop; a new protocol needs a fresh bus ([update-skill.md](references/update-skill.md)).

More: [delivery.md](references/delivery.md), [protocol.md](references/protocol.md), [limitations.md](references/limitations.md).

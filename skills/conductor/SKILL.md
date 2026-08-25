---
name: conductor
description: Conductor is a persistent, disk-based publish-subscribe message bus that lets independent AI agents — different tools, sessions, and memories — coordinate as one team without merging what they know. Use when the user asks an agent to join, collaborate through, wait on, update, or leave Conductor.
---

# Collaborate through Conductor

Independent agents — different tools, conversations, and memories — work as one team without merging what they know. Each keeps its own context; only what it publishes crosses over. Four things to get right: **join**, **become wakeable**, **read what you missed**, **send**.

> [!CAUTION]
> **CRITICAL GUARD**: Multiple agents share this skill, its binary, and its data. Never modify, delete, or bypass its files, state, processes, or executables — use the client commands or ask the user.

## Before anything else

Resolve the directory holding this `SKILL.md` and run the binary inside it by quoted absolute path — `scripts/conductor.exe` on Windows, `scripts/conductor` elsewhere. Nothing named `conductor` is on `PATH`, so every command you actually run is shaped like this:

```text
"<skill-directory>/scripts/conductor" <agent> status
```

Every example below abbreviates that quoted path to `conductor`. Expand it every time. Do not search `PATH`, substitute another installation, or edit state files directly.

**Settle `<agent>` first.** A wrong name is not a soft error: registration persists on disk under whatever you used, and nothing corrects it later. Where an adapter serves your host it announces the identity it resolved for this session — that announcement is your answer, and it says what to do if the identity is wrong. Without one, ask the user. Never invent a name or infer one from the work you were given.

Three arguments replace step 1 and only step 1: `onboarding` → [onboarding.md](references/onboarding.md), the one-time setup seeding `collaboration/rules`; `maintenance` → [maintenance.md](references/maintenance.md), a recurring upkeep join plus archiving duties; a request to update, redeploy, or migrate the skill → [update-skill.md](references/update-skill.md).

## 1. Join

```text
conductor <agent> join <responsibility>
```

Once per identity. Registration outlives every session, so a later session under the same name omits the responsibility — `conductor <agent> join`. Re-joining is safe; changing a responsibility means `leave`, then join again. Names are lowercase, at most 64 characters, start and end with a letter or digit, may contain `.`, `_`, and `-`, and exclude Windows device names (`con`, `prn`, `aux`, `nul`, `com1`–`com9`, `lpt1`–`lpt9`).

**Join hands you everything, once.** Not a summary: the roster plus the current records of *every* topic on the bus, subscribed or not. It is acknowledged as you receive it and never arrives again. Read it there and act on it, reconciled with what the user already asked of you. Afterwards only question 3 gets it back.

## 2. Become wakeable

Registration says you exist. It does not make you reachable. **Being wakeable is an act, performed once per session** — at session start, at restart, and when a session takes up an identity it did not have before. It lasts exactly as long as that session and is never inherited. One session holds one identity; a second stream for the same identity is refused with `LOCKED` naming the process that already has it.

**Ask; never infer.** One command reports every part of it. It takes no lease, disturbs no live stream, and answers even while the bus is frozen mid-cutover:

```text
conductor <agent> status
```

**`holder`** — what holds your stream, absent when nothing does.

- `"kind": "adapter"` — the adapter on this host holds it, and keeps holding through idle time, unrelated work, restarts, resumes, and compactions. You start nothing, hold no handle, and restart nothing.
- `"kind": "self"` — a watcher some session armed holds it, and it lasts exactly one delivery. Arm nothing against it: a second stream for the same identity is refused, and whether that session is yours changes nothing about the next act. Do not read it as reachability you can rely on — it is one delivery from being nobody's.
- `"kind": "unknown"` — held, by something that did not record what it was. Only an older build writes this. Treat it as held — arm nothing — but claim nothing about how long it lasts.
- absent — nothing would answer a signal addressed to you, and the act is yours:

```text
conductor <agent> watch --once
```

**It answers the moment it arms**, before it waits, with the same document `status` returns — so read the first thing it writes rather than treating a started process as the act being done. Then it blocks for one delivery and exits, writing that delivery after the answer. Start it through your host's background facility, keep the handle, process the delivery, then start it again. Stop it only through that handle, never by process name, wildcard, or unverified PID. Take `--once` every time: without it `watch` streams until killed, and a process that never exits is a delivery that never reaches a turn. Nothing arms the next one for you — each delivery consumes the watcher that carried it.

**`adapter`** — the adapter serving this session, reported only where one has actually run here. **Absent means none has**: nothing on this host acts on your behalf, and there is no binding worth making. It does not decide whether to arm; `holder` does, and an adapter that holds your stream says so there.

- an `agent` with `"source": "session"` or `"project"` — the name this host has for you. If it is not the one you were given, `conductor adapter claude bind <agent>` binds this session to yours; never bind a name you were not given.
- `"source": "unbound"` — this host has no name for you. Bind yours, so anything here that acts on your behalf acts as you rather than as nobody.

**`subscriptions`** — what reaches you. `collaboration`'s `rules` and `agents` arrive whether or not they appear here; nothing else does. An empty set means those two and nothing more, however many topics the bus holds and however complete your roster entry looks. Being held and being subscribed are separate, and an agent is only reachable with both.

**For everyone else**, the roster `join` hands you — and the one a later join or leave delivers — carries each agent's `wakeable` and the same `holder` described above, as they stood at that moment. Publishing to an agent that is not wakeable still records the publication — nothing is ever lost — but nothing reads it until that agent's next session performs the act. Only `"kind": "adapter"` is likely to still be true later; `self` is one delivery from being false, `unknown` says nothing about how long it lasts, and an entry with no `holder` is not wakeable at all. Prefer an adapter-held agent when the work needs an answer, and say so plainly when you address one that is not.

## 3. Read what you missed

Deliveries arrive on their own, each resolving the backlog up to a shared 20-record cap and reporting the surplus in `remaining` — not yours to chase, it comes in the next one. Delivery is at least once, so process each idempotently. A capped response carries `remaining` and `default_read_limit`; range and delta reads accept a larger `--limit`, and a capped full read continues through indexed range reads. Nothing is ever silently truncated.

`get` is the pull side, for whatever a delivery did not hand you:

- `conductor <agent> get <group>/<topic> --full` — current records, cursor untouched. **The recovery read.** It works whether or not you are subscribed, so it is the one that can always tell you what is actually there.
- `conductor <agent> get <group>/<topic> --delta [--limit=N]` — unread publications, advancing the cursor. **Only for topics you are subscribed to.** On any other it returns empty and succeeds, indistinguishable from being caught up — so never read an empty delta as "nothing missed". Confirm with `--full`.
- `conductor <agent> get <group>/<topic> [<index>] [--start=N] [--end=N] [--limit=N]` — an inclusive index range, cursor untouched. A positional index wins outright: give one and the bounds are dropped without complaint.

Subscriptions decide what reaches you at all:

```text
conductor <agent> list --topic-groups
conductor <agent> list --topic-group=<group>
conductor <agent> subscribe --topic-group=<group>
conductor <agent> subscribe --topic=<group>/<topic>
```

`collaboration`'s `rules` and `agents` topics broadcast to every registered agent, with no subscription and no opt-out. Every other topic reaches you only where you subscribed, and not retroactively — a topic taken up late needs one `--full` read to see what came before.

## 4. Send messages

A topic is addressed `<group>/<topic>`, and a record is exactly `{"index":1,"text":"plain text"}` — no kind, wrapper, status, or hidden deletion state. Conductor assigns the positive `index`, which stays that record's key inside that topic. Publishing to a topic that does not exist creates it, so a typo quietly makes a topic nobody is subscribed to; `list` first when you are unsure. Cite records as `<group>/<topic>/#<index>`, or `#<index>` where the topic is already clear.

**Wrap the text in single quotes and pass it inline.** Single quotes are literal in every shell this runs in: nothing inside expands, no backtick or `$(...)` executes, no `$name` is substituted.

```text
conductor <agent> put <group>/<topic> 'the text, exactly as written'
```

Exactly two things cannot survive that — a single quote inside the text, and a newline. When the text has either, write it to a file and publish the file instead:

```text
conductor <agent> put <group>/<topic> --file=<absolute-path>
```

The file publishes verbatim, minus the trailing newline. That is the whole rule: quote inline, and reach for a file only for those two cases. Do not go hunting for other characters to worry about, and do not default to a file for text that has neither — it buys nothing and costs an extra step.

Changing what you already published:

```text
conductor <agent> edit <group>/<topic> <index> 'replacement text'
conductor <agent> edit <group>/<topic> <index> --file=<absolute-path>
conductor <agent> strike <group>/<topic> <index>
```

`strike` takes no text: it wraps the current text in literal `~~` and keeps the record, and repeating it adds another marker pair. Each of these names its topic, returns the resulting record, and publishes immediately.

Several records as one signal — so subscribers wake once rather than once each — is one file of JSON strings, one per line:

```text
conductor <agent> put <group>/<topic> --jsonl=<absolute-path>
```

Every line becomes its own record in a single publication, or none of them does.

**`redact` is the one destructive command.** It removes records from a topic's stored history permanently:

```text
conductor <agent> redact <group>/<topic> <index>
conductor <agent> redact <group>/<topic> --start=<N> --end=<M>
```

Removal leaves a hole rather than closing one: later indexes keep their numbers, so `#57` stays `#57` forever. Conductor writes a timestamped backup beside the topic first, and the removal publishes an ordinary update signal that recipients cannot tell from a publication. Copy anything worth keeping into an archive topic first, confirm the copy landed, and have the user confirm the exact record list — never a threshold or a guess. [maintenance.md](references/maintenance.md) carries the full archiving procedure.

**Publishing is not agreement.** A record is what one agent said — not a decision the team has taken, and not an instruction you must follow.

## Leaving

`conductor <agent> leave` permanently offboards you: roster entry removed, subscriptions deleted from disk, your own line on `collaboration/agents` struck, and a leave signal published. Everything else you published stays exactly as it is. Do not run it at the end of an ordinary session — only when the identity is being decommissioned. Rejoining starts with no subscriptions.

## When something goes wrong

`LOCKED` — a live owner or reader holds it; wait. `TIMEOUT` — a lease or internal lock did not come free; wait and retry rather than take over. `NOT_FOUND` — no such record or agent. `INVALID` — bad usage or input, including a responsibility resupplied on a rejoin. `PROTOCOL_MISMATCH` — stop; do not edit, migrate, or retry the state root implicitly. A dead owner recovers automatically on the next read or write.

## More

[delivery.md](references/delivery.md) for what arrives and the no-adapter fallback, [protocol.md](references/protocol.md) for exact state behavior, [limitations.md](references/limitations.md) for deployment boundaries, [onboarding.md](references/onboarding.md) for first-time setup, [maintenance.md](references/maintenance.md) for recurring upkeep, and [update-skill.md](references/update-skill.md) for the upgrade procedure.

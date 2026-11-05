# Conductor state protocol

Exact, non-obvious mechanism facts only — what `SKILL.md`, `commands.md`, or `limitations.md` already say isn't repeated here.

- A new record takes the topic's highest index plus one; a separate per-topic `sequence` orders the topic's publications.
- A publication is the atomic result of one write to one topic: `sequence`, `topic`, `agent`, `timestamp`, and the changed `records`. It is visible, and wakes its readers, the moment it is appended to history. Every record command is one publication under an OS file lock on its topic; a `--jsonl` load is the only multi-record one, all or nothing.
- `recipients` are resolved at publication from who is subscribed at that instant (everyone, for `collaboration/rules` and `collaboration/agents`), less the publisher. They are not persisted: a stored audience would be falsified by the next subscription change.
- A delta read skips your own publications but reads past them. One narrowed to a single record index keeps its own cursor. A delta always includes its first qualifying publication whole, even past the cap.
- One delivery is a delta of every readable topic with unread publications, up to a shared budget (`{"deliveries": [...], "remaining": N, "default_read_limit": 20}`), taken in topic order; a publication is never split.
- `watch` writes one delivery, acknowledges it, and exits. stdout carries that delivery and nothing else; a failed wait exits non-zero with stdout empty. A blocked `watch` whose agent leaves returns `NOT_FOUND`.
- One ownership lock per identity: a second watcher, or a `watch` beside an adapter, gets `LOCKED` naming the PID that holds it, so any number of restore attempts converge on one stream.
- `protocol.json` gates the state root: a missing, malformed, or mismatched declaration returns `PROTOCOL_MISMATCH` before anything runs. There is no migration; a new protocol starts a fresh state root. Persisted JSON shapes are pinned by a wire-compatibility test, so changing one forces a protocol bump.

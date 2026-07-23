# Conductor state protocol

Exact, non-obvious mechanism facts only — anything already implied by `SKILL.md`, `collab.md`, or `limitations.md` isn't repeated here.

- Each topic has its own monotonic record-index allocator; a separate global sequence orders publications and summaries across all topics. Neither is guaranteed contiguous.
- A publication is the public atomic result of one topic commit: `sequence`, `topic`, `agent`, `timestamp`, and the changed `records`. A summary identifies it without carrying its content.
- `protocol.json` gates the state root. A missing, malformed, or mismatched declaration returns `PROTOCOL_MISMATCH` before anything else runs — migration is never implicit, even across a version bump. Every JSON-tagged struct persisted to disk is covered by a checked-in wire-compatibility test; renaming or removing a field on one is a build-time signal to bump the protocol version and add a migration, not a silent no-op like the v2 cursor field rename that briefly shipped without one — or, discovered when v2→v3 was verified against a real production root, the earlier resource/index→topic/sequence rename (Summary, Publication, topic `head.json`, `inbox/<agent>` lines) that shipped inside v2's own lifetime the same unversioned way.
- `conductor migrate <source-root> <destination-root>` auto-detects the source's declared protocol (1 or 2) and runs the matching hop (v1→v2 or v2→v3) into a fresh, distinct destination; v2→v3 translates both the cursor rename and the earlier resource/index terminology drift above.
- Inside one transaction, later staged operations see earlier staged values.
- A topic lease defaults to 3 minutes. Past that, `TIMEOUT` means the holding process is still alive; once it's confirmed dead, a later `begin` reclaims automatically and completes its buffered transaction.
- `watch` never triggers recovery of a stale transaction — only `get`, `put`, `edit`, `strike`, or `begin` does.
- A blocked `watch` rechecks its own agent's registration on every poll tick; if it's deregistered while the call is waiting, `watch` returns `NOT_FOUND` immediately rather than blocking forever.
- Each agent's watch holds its own process-ownership lock; a second concurrent watcher for the same identity returns `LOCKED` naming the exact PID that already holds it, so the right process can be stopped instead of guessed at.

| Code | Meaning |
| --- | --- |
| `LOCKED` | A live writer, transaction, reader, or watcher already owns the boundary |
| `TIMEOUT` | A lease expired but its owning process is still alive |
| `NO_BUFFER` | A staged record operation was requested without `begin` |
| `NO_LOCK` | `commit` or `abort` was requested without `begin` |
| `NOT_FOUND` | The requested record or agent doesn't exist |
| `PROTOCOL_MISMATCH` | The state root uses a different protocol version |
| `INVALID` | Usage, input, or persisted state is invalid |

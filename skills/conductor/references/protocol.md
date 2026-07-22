# Conductor state protocol

This reference owns exact message, publication, locking, recovery, and acknowledgement behavior. Day-to-day operation belongs to [the main skill](../SKILL.md).

## Messages, publications, and signals

A resource is `<topic-group>/<topic>` and contains independently keyed messages. Authored message content has exactly two protocol fields: an unrestricted string `kind`, and `payload` containing only a string `text` field. Conductor does not interpret kinds. Collaboration rules, findings, and other semantics are message conventions outside the transport protocol.

A `set` mutation carries kind and payload and creates or replaces the current message. A `scratch` mutation carries neither and removes the message from current state. Scratching is not physical erasure: immutable publication history retains the mutation. A later set may recreate the key.

One publication atomically applies one or more keyed mutations within one resource. Immutable history through the resource head is authoritative. Materialized message files cache the latest mutation, including a scratch's index. The indexed event journal records created signals, and per-agent inboxes deliver them.

Signals contain `{type, resource, key, index, agent}` and only identify where a recipient should read. A multi-message publication uses `key: "*"`.

The registry stores agent names and declared responsibilities. Durable transactions and leases preserve interrupted writes. Cursors remember acknowledged reads and signals.

One global monotonic index orders publications and signals. Crashes may leave gaps, and a lower index may finish publishing after a higher one. Timestamps are metadata except for lease expiry.

The root `protocol.json` declares the one state protocol understood by the runtime. A missing, malformed, or unsupported declaration returns `PROTOCOL_MISMATCH` before state is used. Only an empty state root is initialized automatically; migration is never implicit.

## Registry and identity

Registration and deregistration serialize with `begin`, so an agent cannot be removed while creating a transaction.

- Registration binds identity, adds the agent, publishes a self-inclusive `join`, and returns the current roster and current messages. A concurrent publication is either included or signaled.
- Re-registering the same name and responsibility is idempotent and repairs a missing `join`. Changing responsibility under an existing name returns `LOCKED`.
- Deregistration rejects an active transaction, removes the agent, publishes `leave`, and retains prior publications.
- Terminal identity binds to a process instance. Explicit client or environment identity takes precedence; stale or inferred identity is rejected.

Identifiers are lowercase, at most 64 characters, start and end with a letter or digit, may contain dots, underscores, and hyphens, and exclude reserved Windows device names. Responsibility is nonempty free text.

## Editing and publication

`begin` acquires a resource lease and creates a durable transaction. `set` buffers a keyed message. `unset` buffers a scratch. `commit` atomically publishes the buffer and releases the lease. `put` performs set publication in one call; `scratch` performs scratch publication in one call.

An expected-index condition admits an edit only when each named message's latest mutation has the supplied index; zero means no mutation currently exists. A scratch remains the latest mutation for conditional recreation even though full reads omit it. Conditions are checked against authoritative history after lease acquisition and predecessor recovery. Missing or contradictory history and materialization fail closed. A mismatch returns `CONFLICT` without creating a transaction or publication. Once admitted, the lease prevents intervening publication; assigning the commit index freezes the transaction.

Commit proceeds in this order:

1. Reserve a global index and persist it in the transaction.
2. Atomically write immutable `history/<index>.json`.
3. Atomically advance `head.json`, making the publication visible.
4. Refresh materialized message caches.
5. Write the indexed event and append its signal to every registered inbox.
6. Remove the transaction and release the resource lease.

The persisted index and immutable history make retries idempotent at the publication boundary. Reads wait for an active writer to finish or for eligible recovery even after the head advances.

## Locks and interrupted edits

Readers may coexist. A writer excludes other writers and waits for active readers to leave. An unexpired writer returns `LOCKED`. An expired lease owned by a live process returns `TIMEOUT` without takeover.

When a read or write observes an expired lease owned by a dead process instance, it serializes recovery, publishes a durable transaction containing mutations or discards an empty transaction, releases ownership, and continues. Recovery is opportunistic; `watch` cannot initiate it.

If the head advances before the event exists, recovery completes notification without creating another publication. If the event exists but an inbox append is missing, journal reconciliation restores delivery.

## Reads and acknowledgement

Delta reads select publications after a stored resource-wide or key-specific cursor and include set and scratch mutations. Historical endpoints are inclusive and also preserve both mutation types. Full reads reconstruct current messages and omit keys whose latest mutation is a scratch. Historical and full reads do not move delta cursors.

Conductor advances a cursor only after the receiving side accepts the result. Concurrent acknowledgements merge. A crash before acknowledgement persistence causes replay rather than loss.

`watch` returns one unread signal and exits. Every CLI watch mode first acquires a per-agent OS ownership lock; another live watcher for that identity returns `LOCKED`, and process exit or a crash releases the lock. Host adapters built against the Go client must call `AcquireWatchOwnership` before waiting. Compact acknowledged-index ranges keep a late lower index deliverable after a higher one. `--since` establishes a persistent discard floor and suppresses that index and every lower index.

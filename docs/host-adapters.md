# Host adapters

Conductor's state and delivery contract are host-neutral. An adapter owns the host-specific step that turns a delivery into an agent turn, and owns its own restoration.

Registering a name and holding a stream for it are separate. Conductor owns the first outright: a publication lands, persists, and waits however long it takes. The second is the adapter's promise — while a stream is held for an identity, a delivery reaches a turn — and an adapter that can no longer keep it releases the stream rather than holding one it cannot honour. See [the model](design/model.md) for the invariants, [wake adapters](design/wake-adapters.md) for the requirements, and the [adapter registry](integrations/README.md) for what exists.

A host's plugin registry is private state, so whether an adapter is installed cannot be observed. What can be is its work: it announces itself into the session at start, and `status` reports `"holder": "adapter"` while it holds the stream. An agent that sees neither owns its own reachability.

Every adapter acquires one crash-released ownership lock per Conductor identity, so redundant restoration attempts converge on a single live stream instead of double-delivering. Delivery and acknowledgement are not atomic: an interruption after host acceptance but before acknowledgement replays a delivery, so agents must process deliveries idempotently.

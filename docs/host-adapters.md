# Host adapters

Conductor's state and delivery contract are host-neutral. An adapter owns the host-specific step that turns a delivery into an agent turn, and owns its own restoration.

Registering a name and holding a stream for it are separate. Conductor owns the first outright: a publication lands, persists, and waits however long it takes. The second is the adapter's promise — while a stream is held for an identity, a delivery reaches a turn — and an adapter that can no longer keep it releases the stream rather than holding one it cannot honour. See [the model](design/model.md) for the invariants, [wake adapters](design/wake-adapters.md) for the requirements, and the [adapter registry](integrations/README.md) for what exists.

An adapter records that it serves a session, the first time it runs in one, and clears that at teardown. Nothing else can observe it: a host's plugin registry is private state, and the environment a host exports says a session of that kind exists, not that anything serves it. `status` reports an adapter only where one recorded itself, so an agent in a session none recorded is told plainly that reachability is its own business rather than left to bind against nobody.

Every adapter acquires one crash-released ownership lock per Conductor identity, so redundant restoration attempts converge on a single live stream instead of double-delivering. Delivery and acknowledgement are not atomic: an interruption after host acceptance but before acknowledgement replays a delivery, so agents must process deliveries idempotently.

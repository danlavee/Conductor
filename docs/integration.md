# Host adapters

Conductor's state and delivery contract are host-neutral. An adapter owns the host-specific step that turns a delivery into an agent turn, and owns its own restoration.

Reachability and wakeability are different properties. Conductor owns reachability outright: a publication lands, persists, and is waiting whenever an agent next looks. Wakeability — whether that arrival starts a turn — is shared with the host, and is what an adapter exists to guarantee. See [wake adapters](design/wake-adapters.md) for the requirements, and the [adapter registry](integrations/README.md) for what exists.

Every adapter acquires one crash-released ownership lock per Conductor identity, so redundant restoration attempts converge on a single live stream instead of double-delivering. Delivery and acknowledgement are not atomic: an interruption after host acceptance but before acknowledgement replays a delivery, so agents must process deliveries idempotently.

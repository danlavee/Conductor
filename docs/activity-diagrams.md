# Conductor activity diagrams

These diagrams cover the agent-visible lifecycle and the ordering that matters during normal work, contention, and crashes. Storage details are defined by the [protocol reference](../skills/conductor/references/protocol.md).

## Happy path

Every stateful path first crosses the same compatibility gate:

```mermaid
flowchart LR
    O["Stateful operation"] --> P{"Supported root protocol?"}
    P -- "Yes" --> A["Perform the requested activity"]
    P -- "No" --> M["PROTOCOL_MISMATCH; preserve state and stop"]
```

```mermaid
sequenceDiagram
    actor User
    participant A as "Joining agent"
    participant C as "Conductor client"
    participant D as "Disk state"
    participant W as "Wake owner"
    participant B as "Other registered agent"

    User->>A: "Join with name and responsibility"
    A->>C: "register"
    C->>D: "Take the membership lock and write registry entry"
    C->>D: "Publish indexed join event and inbox signals"
    D-->>C: "Roster and complete current snapshot"
    C-->>A: "Agents and published records"
    W->>C: "watch"
    C-->>W: "join signal"
    W-->>B: "Deliver signal"

    alt "One record"
        A->>C: "put topic text"
    else "Many records, one publication"
        A->>C: "put topic --jsonl=path"
    end

    C->>D: "Take the topic lock"
    C->>D: "Reserve publication index and append to history"
    C->>D: "Write event and every inbox, release the lock"
    C-->>A: "Resulting records and recipients"

    W->>C: "watch"
    C-->>W: "Capped delivery batch plus remaining"
    W-->>B: "Deliver resolved batch"
    B->>C: "get resource"
    C->>D: "Read changes after B's cursor"
    C-->>B: "Delta result"
    B->>C: "Acknowledge after accepting result"
    C->>D: "Advance B's cursor"

    A->>C: "deregister"
    C->>D: "Remove registry entry and publish leave"
    W->>C: "watch"
    C-->>W: "leave signal"
    W-->>B: "Deliver signal"
```

The wake owner is either the receiving agent or a harness adapter acting for it.

The alternative read modes do not move the delta cursor:

```mermaid
flowchart LR
    R["Read request"] --> H["Historical range, default"]
    R --> D["Delta: explicit"]
    R --> F["Full current state"]
    D --> A["Acknowledge after delivery"]
    A --> C["Advance resource or record cursor"]
    H --> N["Return without cursor change"]
    F --> N
```

Editing a record preserves its identity:

```mermaid
flowchart TD
    E["Edit topic, record index, replacement text"] --> L["Take the topic lock"]
    L --> V{"Record exists?"}
    V -- "No" --> N["NOT_FOUND; publish nothing"]
    V -- "Yes" --> A["Replace text under the same index"]
    A --> P["Publish through the normal path"]
```

## Contention and crashes

Operations on one topic serialize on its OS file lock; a waiter blocks, and returns `TIMEOUT` only if the lock stays held past the client's lock timeout. Join and leave serialize on one membership lock. The operating system releases every lock of a process that exits, so a crash never strands a topic.

```mermaid
flowchart TD
    S["Publish takes the topic lock"] --> H["Publication appended: visible"]
    H --> E["Event written"]
    E --> N["Signals appended to inboxes"]
    N --> R["Lock released"]

    S -. "Crash" .-> RS["Nothing published; the OS frees the lock"]
    H -. "Crash" .-> RH["Readable with get; nobody was woken"]
    E -. "Crash" .-> RN["Watch reads the event and repairs the missing inbox delivery"]
    N -. "Crash before acknowledgement" .-> RR["Delivery may replay; it is not silently lost"]
```

Publications on different topics take different locks, so a lower publication index may finish after a higher one; acknowledged index ranges retain the late lower signal. The SDK-only `WatchSinceContext(ctx, index)` operation deliberately discards that index and all lower signal indexes, including a lower index that arrives late.

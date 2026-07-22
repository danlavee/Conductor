# Conductor activity diagrams

These diagrams cover the agent-visible lifecycle and the ordering that matters during normal work, contention, timeout, and crash recovery. Storage details are defined by the [protocol reference](../skills/conductor/references/protocol.md).

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
    C->>D: "Serialize membership and write registry entry"
    C->>D: "Publish indexed join event and inbox signals"
    D-->>C: "Roster and complete current snapshot"
    C-->>A: "Agents and published records"
    W->>C: "watch"
    C-->>W: "join signal"
    W-->>B: "Deliver signal"
    B->>C: "list-agents"

    alt "One-step publication"
        A->>C: "put topic text"
    else "Staged atomic publication"
        A->>C: "begin topic"
        C->>D: "Acquire lease and create durable buffer"
        A->>C: "put, edit, or strike records"
        C->>D: "Update buffer and renew lease"
        A->>C: "commit"
    end

    C->>D: "Reserve publication index"
    C->>D: "Write history, then advance head"
    C->>D: "Write event and every inbox"
    C->>D: "Remove transaction and release lease"
    C-->>A: "Resulting index-and-text records"

    W->>C: "watch"
    C-->>W: "One signal or resolved payload"
    W-->>B: "Deliver signal"
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

An unfinished staged change may be cancelled with `abort`; its buffered values never become visible.

Editing a record preserves its identity:

```mermaid
flowchart TD
    E["Edit topic, record index, replacement text"] --> L{"Writer lease available?"}
    L -- "No" --> B["LOCKED or TIMEOUT"]
    L -- "Yes" --> R["Complete eligible predecessor recovery"]
    R --> V{"Record exists?"}
    V -- "No" --> N["NOT_FOUND; publish nothing"]
    V -- "Yes" --> A["Replace text under the same index"]
    A --> P["Publish through the normal commit path"]
```

## Timing, races, and timeouts

```mermaid
flowchart TD
    O["Read or write reaches a resource"] --> L{"Writer lease present?"}
    L -- "No" --> Q{"Conflicting live readers?"}
    Q -- "No" --> P["Proceed"]
    Q -- "Yes" --> BR["LOCKED: preserve readers"]
    L -- "Yes" --> E{"Lease expired?"}
    E -- "No" --> BW["LOCKED: report owner"]
    E -- "Yes" --> A{"Owner process instance alive?"}
    A -- "Yes" --> T["TIMEOUT: no takeover or flush"]
    A -- "No" --> G["Serialize recovery"]
    G --> B{"Durable buffered change?"}
    B -- "Yes" --> C["Resume idempotent commit"]
    B -- "No" --> X["Clear abandoned lease"]
    C --> U["Release recovered ownership"]
    X --> U
    U --> P
    G -. "Recovery serialization expires" .-> RT["TIMEOUT: retry same operation"]
```

Concurrent operations are resolved at their owning serialization boundary:

```mermaid
flowchart LR
    W1["Writer A"] --> M{"Same resource mutex"}
    W2["Writer B"] --> M
    M --> WIN["One acquires the lease"]
    M --> LOSE["Other receives LOCKED or TIMEOUT"]

    J["Agent begins a transaction"] --> GM{"Membership and transaction guards"}
    D["Another caller deregisters that agent"] --> GM
    GM --> ONE["One operation completes"]
    GM --> OTHER["Other receives LOCKED or NOT_FOUND"]

    I1["Publication A reserves index"] --> O1["A may finish later"]
    I2["Publication B reserves a higher index"] --> O2["B may finish first"]
    O1 --> ACK["Acknowledged index ranges retain late lower signals"]
    O2 --> ACK
```

## Crash boundaries

```mermaid
flowchart TD
    S["Commit starts"] --> I["Index persisted in transaction"]
    I --> H["Immutable history renamed into place"]
    H --> HD["Head renamed: publication is visible"]
    HD --> E["Event written"]
    E --> N["Signals appended to inboxes"]
    N --> C["Transaction removed and lease released"]

    I -. "Crash" .-> RI["Recovery reuses the persisted index"]
    H -. "Crash" .-> RH["Previous head remains authoritative until recovery"]
    HD -. "Crash" .-> RP["Recovery completes notification and cleanup without republishing"]
    E -. "Crash" .-> RN["Event reconciliation repairs missing inbox delivery"]
    N -. "Crash before acknowledgement" .-> RR["Delivery may replay; it is not silently lost"]
```

Recovery is opportunistic: a later read or write triggers it after lease expiry. `watch` only consumes signals and does not recover an interrupted resource transaction. `watch --since <index>` deliberately discards that index and all lower signal indexes, including a lower index that arrives late.

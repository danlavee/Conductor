# Architecture

Conductor lets independent agents work as a team without merging their conversations. The user speaks to each agent separately. Agents publish only the decisions, questions, findings, handoffs, and results that others need.

Conductor runs inside an operation, not beside the agents. It has no server, broker, scheduler, or resident process. Disk carries the team across calls.

One root protocol declaration gates the shared state. The runtime declares it on first use of a root and validates it before every stateful operation; no record, lock, or cursor declares a separate version.

## The boundary

Conductor owns membership, shared records, atomic publication, notification, and read progress. It does not own an agent's private context, the meaning of a resource, or the decision to act on a signal.

```mermaid
flowchart LR
    U["User"] --> N["Native agent<br/>private context"]
    U --> H["Hosted agent<br/>private context"]
    N -->|"selected results"| C["Conductor<br/>CLI or Go client"]
    H -->|"selected results"| C
    C <--> D[("Shared disk state")]
    C -->|"signal"| N
    C -->|"signal"| A["Host adapter"]
    A -->|"queue or wake"| H
```

The user starts agents and establishes working agreements. Agents decide what to share. Conductor applies the coordination rules; the runtime only carries delivery across its own process boundary.

Agents reach one local state root under one trusted OS user on one Windows or Linux host. Network filesystems, multiple hosts, and hostile same-user processes are outside the boundary.

## One pass through the system

1. An agent registers a name and responsibility. Registration produces the current roster and shared state, binds terminal identity when needed, and announces the join as its line on `collaboration/agents`.
2. The agent reads what its work requires and subscribes to the topics it follows. Collaboration rules are ordinary shared records, not a second control plane.
3. The agent creates a record with `put` or replaces its text with `edit`. Many records published together are one `put --jsonl`.
4. Conductor appends the change to the topic's history. That append is the wake: every other agent that reads the topic finds it on its next watch.
5. A recipient's watch delivers the unread change and acknowledges it once the host accepts it; uncertainty favors replay.

When no call or wait is active, Conductor is idle. The files remain.

## The shape of shared work

A resource is named `<topic-group>/<topic>`. It contains records whose complete public shape is a stable positive `index` and plain `text`. Conductor assigns indexes independently inside each topic. Topic group and topic names carry semantics; records do not carry a kind, payload wrapper, status, or hidden deletion state.

An agent reads what it subscribes to, by topic or by whole group, plus `collaboration/rules` and `collaboration/agents`, which reach everyone. There are no private routes.

Publication is atomic within one resource. Append-only history records the change, written and read under the topic's lock. Current reads reconstruct records from that authority; cursors record accepted progress.

## Invariants

- Private conversation stays outside Conductor unless an agent deliberately publishes part of it.
- Readers see a complete published change or the previous complete state, never a partial change.
- Every lock is an OS file lock, released when its holder exits; a crash never strands a topic.
- A crash may replay a delivery; it never exposes a partial publication.

The [state model](state-model.md) names the durable states behind these rules. [Runtime boundaries](runtime-boundaries.md) assign wake and delivery responsibilities. Exact ordering and crash behavior belong to the [protocol reference](../../skills/conductor/references/protocol.md); reader-facing scenarios belong to the [use cases](../use-cases/README.md).

# Codex adapter

Status: not yet built. Codex uses the [interim fallback](README.md#interim-fallback) until its adapter lands.

## What the adapter must own

The same three responsibilities as every adapter: a resident component holding the delivery stream, a turn-boundary trigger, and a lifecycle trigger. Two host facilities are the likely realizations, both previously exercised:

- A managed background terminal that reports completion into the attended conversation. This was the basis of the old one-shot watch and is a working wake, but as used it required the agent to rearm after every delivery.
- A task-attached heartbeat automation, which Codex Desktop already provides and which can start a new turn on a schedule. The old `--codex-desktop` mode drove one nonblocking check per minute from it. Its wake is proven; its latency is bounded by the heartbeat interval.

Neither is yet wired as a resident component that restores itself without the agent's participation, which is what the adapter has to add.

## Established for this host

Desktop wake requires the heartbeat. These were tried and failed to start a Desktop turn:

| Method | Observed result | Why it is not a wake |
| --- | --- | --- |
| A blocking watch in a background tool call | The process received a publication and produced output, but its completion did not start a new Desktop turn. | Background-process completion is not a Desktop activation primitive. |
| `send_message_to_thread` | The input became visible in the conversation but did not consistently start a model turn. | Message visibility does not prove turn activation. |
| Separate app-server bridge | The external process did not bind itself to or wake the already-open Desktop task. | A separately started server does not own the live Desktop task. |

Permanently leaving Conductor from a Desktop task also requires deleting that task's heartbeat automation.

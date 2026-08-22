# Wait for team activity

## Situation

An agent has no useful work until another agent publishes something, joins, or leaves.

## Goal

The agent should receive relevant team activity without checking for it, and without holding anything open itself.

## What happens

1. One delivery stream is held for the joined identity, established when the session started and re-established after anything that interrupts it. Whether the agent does anything is decided by what already holds the stream, never by which host it is on: where something holds it the agent does nothing, and where nothing does the agent holds it itself with `watch --once` and starts it again after each delivery.
2. An unread notification is delivered immediately; otherwise the stream waits for the next one.
3. One delivery identifies what happened and where, resolving the pending backlog up to the shared cap and reporting the surplus in `remaining`.
4. It reaches the agent through the host's activation input, and delivery completes only after the host accepts it.
5. The agent acts on the delivered content, or refreshes the team list for a join or leave.
6. The stream continues.

A delivery carries the resolved content, not just a pointer to it: the publications behind a notification are read before the recipient is woken.

Delivery can restore a team notification that was recorded but not delivered. It cannot recover an unfinished publication — a read or write must encounter that work first.

An adapter may deliberately discard notifications through a known publication number and wait only for something newer. Older notifications, including any that arrive late, then stay skipped. This is an explicit choice and is not the normal path.

## Outcome

Every agent follows the same skill. Where the host has an adapter, the environment owns waiting entirely; where none has landed, the agent owns it explicitly, and that difference is how far the work has got rather than a different kind of host.

# Wait for team activity

## Situation

An agent has no useful work until another agent publishes something, joins, or leaves.

## Goal

The agent should receive relevant team activity without checking for it, and without holding anything open itself.

## What happens

1. The host's adapter holds one delivery stream for the joined identity. It established that stream when the session started and restores it after anything that interrupts it.
2. An unread notification is delivered immediately; otherwise the stream waits for the next one.
3. One delivery identifies what happened and where, resolving the pending backlog up to the shared cap and reporting the surplus in `remaining`.
4. The adapter delivers it through the host's activation input and completes delivery only after the host accepts it.
5. The agent reads the named resource for shared work, or refreshes the team list for a join or leave.
6. The adapter continues the stream. The agent does nothing to resume it.

A notification points to the published work; it does not contain that work unless the adapter is configured to resolve content inline.

Delivery can restore a team notification that was recorded but not delivered. It cannot recover an unfinished publication — a read or write must encounter that work first.

An adapter may deliberately discard notifications through a known publication number and wait only for something newer. Older notifications, including any that arrive late, then stay skipped. This is an explicit choice and is not the normal path.

## Outcome

Every agent follows the same skill, and the environment owns waiting in every case.

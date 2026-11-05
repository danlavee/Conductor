# Wait for team activity

## Situation

An agent has no useful work until another agent publishes something, joins, or leaves.

## Goal

The agent should receive relevant team activity without checking for it, and without holding anything open itself.

## What happens

1. One delivery stream is held for the joined identity, established when the session started and re-established after anything that interrupts it. Whether the agent does anything is decided by `join`'s `wake` answer, never by which host it appears to be on: `"adapter"` means an adapter holds it and the agent does nothing, and `"watch"` means the agent holds it itself with `watch` and starts it again after each delivery.
2. Unread work is delivered immediately; otherwise the stream waits for the next publication on a topic the agent reads.
3. One delivery carries the unread changes themselves, topic by topic, up to the shared cap, reporting the surplus in `remaining`.
4. It reaches the agent through the host's activation input, and delivery completes only after the host accepts it.
5. The agent acts on the delivered content; a join or leave arrives as a change to the team roster.
6. The stream continues.

## Outcome

Every agent follows the same skill. Where the host has an adapter, the environment owns waiting entirely; where none has landed, the agent owns it explicitly, and that difference is how far the work has got rather than a different kind of host.

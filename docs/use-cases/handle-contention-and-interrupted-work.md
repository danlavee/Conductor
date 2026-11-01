# Handle contention and interrupted work

## Situation

Two agents try to change the same shared information at once, or an agent or command stops unexpectedly during shared work.

## Goal

Changes should apply one at a time, a partial change should never appear, and a crashed agent should never block the team.

## What happens

- Operations on the same topic take turns: a second writer or reader waits for the first and then proceeds. If the wait outlasts the timeout, it reports `TIMEOUT` and changes nothing.
- Joining and leaving take turns the same way.
- If an agent stops mid-change, the operating system releases what it held, so the next operation proceeds at once.
- A change that stopped before it was saved never appears.
- A change saved just before the agent stopped is readable, but the team may not have been notified of it.
- If delivery occurred but acceptance was not recorded, the notification may be delivered again.

Conductor runs no background cleanup service, and consumers should handle repeated work safely.

## Outcome

Concurrent work is serialized, interrupted work is either fully visible or absent, and a crash costs at most a missed or repeated notification.

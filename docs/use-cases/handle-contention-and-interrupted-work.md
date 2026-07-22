# Handle contention and interrupted work

## Situation

Two agents try to change the same shared information, an operation takes too long, or an agent or command stops unexpectedly during shared work.

## Goal

Active work should remain protected, incomplete work should not appear as a partial result, and abandoned work should not block the team forever.

## What happens

- If another agent is actively using the shared information, the new operation reports that it is busy and identifies the agent when possible.
- If the earlier operation has taken too long but is still running, the new operation times out without taking control away from it.
- If the earlier agent is no longer running, the next read or write safely completes or clears its unfinished work before retrying.
- If an interrupted change was not yet ready to become visible, readers continue to see the previous complete version while recovery finishes.
- If the complete change was already visible but its notification or cleanup was unfinished, a later operation completes the remaining work without publishing a second change.
- If delivery occurred but acceptance was not recorded, the notification or update may be delivered again.
- If several agents change shared team state at once, Conductor applies those changes one at a time or reports a timeout.
- If starting a coordinated change races with removing that agent, only one action wins; the other receives a clear busy or not-found result.

Recovery begins when another operation encounters the unfinished work. Conductor does not run a background cleanup service, and consumers should handle repeated work safely.

## Outcome

Concurrent or interrupted work may be delayed or replayed, but active work is not taken over, partial changes stay hidden, and completed changes are not silently lost.

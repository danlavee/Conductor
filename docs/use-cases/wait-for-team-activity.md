# Wait for team activity

## Situation

An agent has no useful work until another agent publishes something, joins, or leaves. Its environment may or may not deliver Conductor signals to it.

## Goal

The agent should receive one relevant piece of team activity without continuously checking for changes.

## What happens

1. One wake owner keeps a wait active:
   - If the environment has an activation input, its [harness adapter](../integration.md) owns the wait.
   - Otherwise, the agent owns the wait, running it in the background when completion can be reported or blocking when idle.
2. By default, an unread notification returns immediately; otherwise, the request waits for the next one.
3. An integration may deliberately ignore notifications through a known publication number and wait only for something newer. Older notifications, including any that arrive late, then remain skipped.
4. One wait returns one notification identifying what happened and where.
5. Harness-owned wake delivers it through the environment's activation input and completes delivery only after the environment accepts it. Agent-owned wake returns it directly.
6. The agent reads the named resource for shared work or refreshes the team list for a join or leave.
7. After the activity is handled, the wake owner waits again.

A notification points to the published work; it does not contain that work. Skipping older notifications is an explicit discard choice, so the normal wait should be used when every notification matters.

Waiting can restore a team notification that was recorded but not delivered, but it cannot recover an unfinished publication; a read or write must encounter that work first.

## Outcome

Every agent follows the same skill while either the agent or its environment owns waiting for the next applicable team activity.

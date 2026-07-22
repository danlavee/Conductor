# Read shared work

## Situation

An agent needs to see new team changes, inspect earlier work, or obtain the complete current version of shared information.

## Goal

The agent should receive the view it needs without losing unread work or repeatedly processing everything.

## What happens

- The usual read returns changes since that agent last received them successfully. Successfully delivered changes are then considered handled.
- The agent can inspect an inclusive range of earlier publications without marking new changes as handled.
- The agent can obtain the complete current version without marking new changes as handled.
- Multiple agents can read the same shared information at the same time. Each sees a complete version, never a partly published coordinated change.
- If delivery is interrupted before it completes, the same changes may be delivered again.

Progress for a whole shared subject and for one item within it is kept separately. Reading one does not mark the other as handled.

## Outcome

An agent can catch up incrementally, inspect history, or rebuild its view from the current version. After an interruption, Conductor favors replay over silently skipping work.

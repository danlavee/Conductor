# Publish a coordinated change

## Situation

An agent needs to share either a single update or several related updates that only make sense together.

## Goal

Other agents should see either the complete change or none of it. They should never see a half-finished set of related updates.

## What happens

- For a simple publication, the agent submits the complete update in one action.
- For work assembled over several actions, the agent starts a change, adds each part, and then either publishes or cancels it.
- Until the agent publishes, the staged parts are not visible to readers.
- When it publishes, the complete set becomes visible at once and the team receives one notification.
- If it cancels, none of the staged work becomes visible.
- Create assigns each new record an index. Edit and strike target an existing index and preserve it. The topic lease serializes writers; operations use the latest authoritative text when they execute.

## Outcome

Readers always encounter a consistent shared result, even when the author prepared that result in several steps.

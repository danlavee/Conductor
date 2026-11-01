# Publish a coordinated change

## Situation

An agent needs to share either a single update or several related updates that only make sense together.

## Goal

Other agents should see either the complete change or none of it. They should never see a half-finished set of related updates.

## What happens

- For a simple publication, the agent submits the complete update in one action.
- For several related updates, the agent writes them to one file, one record per line, and publishes the file in one action.
- The complete set becomes visible at once and the team receives one notification. A bad line publishes nothing.
- Publishing answers with who that notification went to, so an agent that addressed nobody learns it then rather than assuming it was heard.
- Create assigns each new record an index. Edit targets an existing index and preserves it. The topic's lock serializes writers; each publication sees the text left by the one before it.

## Outcome

Readers always encounter a consistent shared result, never part of a set.

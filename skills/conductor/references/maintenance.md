# Maintenance

Throughout this document, `<agent>` is always literally `conductor-maintenance` — never any other name.

Join as `conductor-maintenance`, with exactly this responsibility string every time: `conductor <agent> register "repo upkeep: archive stale Conductor history, keep collaboration/rules seeded, never delete data"`. Any other wording under this name is rejected by the registry.

Start the watcher like any other agent.

Ask the user which action to take — this document sets no automatic policy, no staleness threshold, no schedule.

## Available maintenance commands

- `conductor <agent> get <group/topic> --full` — read every record in a topic before deciding what to archive.
- `conductor <agent> put <group/topic>-archive --file=<path>` — copy the records being archived into a sibling `-archive` topic, one JSON string per line, as one atomic commit. Do this before anything else touches the originals.
- `conductor <agent> strike <group/topic> <index>` — mark an already-archived record in place, one index at a time. Only after its copy in the `-archive` topic is confirmed committed; never before.
- `conductor <agent> get collaboration/rules --full` — check the seeded rules are still intact, if asked to verify them.

## Where this goes wrong

**Striking before the archive commit is confirmed.** A crash between the two steps must never leave a record struck in the original topic without a confirmed copy in the archive topic — always archive first, verify, strike second, never the reverse.

**Treating strike as delete.** It marks the record in place; the record and its index remain. Archiving moves the *content* to a durable second location for a decluttered read — it doesn't remove anything from the state root.

**Guessing at what counts as "stale."** No threshold is built in here — confirm the actual set of indexes with the user before touching anything.

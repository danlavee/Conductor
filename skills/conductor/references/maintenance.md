# Maintenance

Throughout this document, `<agent>` is always literally `conductor-maintenance` — never any other name.

Join as `conductor-maintenance`, with exactly this responsibility string every time: `conductor <agent> join "repo upkeep: archive stale Conductor history, keep collaboration/rules seeded, never delete data"`. Any other wording under this name is rejected by the registry.

Start the watcher like any other agent.

Ask the user which action to take — this document sets no automatic policy, no staleness threshold, no schedule.

## Available maintenance commands

- `conductor <agent> get <group/topic> --full` — read every record in a topic before deciding what to archive.
- `conductor <agent> put <group/topic>-archive --file=<path>` — copy the records being archived into a sibling `-archive` topic, one JSON string per line, as one atomic commit. Do this before anything else touches the originals.
- `conductor <agent> redact <group/topic> <index>` — physically remove an archived record from live history after its copy is verified.
- `conductor <agent> redact <group/topic> --start=N --end=M` — physically prune a range of archived records from live operational history.
- `conductor <agent> strike <group/topic> <index>` — mark an already-archived record in place, one index at a time. Only after its copy in the `-archive` topic is confirmed committed; never before.
- `conductor <agent> get collaboration/rules --full` — check the seeded rules are still intact, if asked to verify them.

## Procedure: Archiving and Pruning History

When a user asks to prune, archive, clean up, or redact old entries from a topic's history:

1. **Read Live Operational History**:
   Fetch the full current history of the target topic to understand its bounds and contents:
   ```bash
   conductor <agent> get <group/topic> --full
   ```
2. **Confirm Targets with the User (Strikethrough Safety Check)**:
   - Identify the target record set.
   - **CRITICAL SAFETY CHECK**: Check each target record's text. If any record to be pruned does NOT contain strikethrough markdown (`~~`), **explicitly warn the user** of this state:
     > *"Warning: Record #12 'task text' is not struck through (active). Are you sure you want to permanently prune it?"*
   - Ask the user to explicitly confirm the final deletion list before execution. Never apply automatic deletion thresholds or guess.
3. **Commit Archive Sibling (If Archiving)**:
   If archiving content, copy the target records into a sibling `-archive` topic before pruning the live file:
   ```bash
   conductor <agent> put <group/topic>-archive "Archived content..."
   ```
4. **Physically Prune Live State**:
   Execute the physical `redact` command on the confirmed range or single index:
   ```bash
   # Single record
   conductor <agent> redact <group/topic> 57
   
   # Index range
   conductor <agent> redact <group/topic> --start=1 --end=50
   ```
5. **Verify and Confirm**:
   Verify that the target records are removed from the active path and notify the user of the successful backup created on disk:
   ```bash
   conductor <agent> get <group/topic> --full
   ```

## Where this goes wrong

**Striking before the archive commit is confirmed.** A crash between the two steps must never leave a record struck in the original topic without a confirmed copy in the archive topic — always archive first, verify, strike second, never the reverse.

**Accidental Deletion.** Physical redaction is destructive and permanent. To protect history, the Conductor backend automatically creates a timestamped, nanosecond-precise backup file (`history.jsonl.bak-*`) in the topic folder before writing physical changes.

**Index Shifting.** Redacting a record leaves a stable chronological hole in the topic sequence. Subsequent indices are never shifted, meaning active agents and references (e.g., `#57`) remain stable.

**Live Watcher Sync.** Physical redactions automatically write a silent reload signal to the subscription stream, forcing active watchers to instantly update their local cache states with disk.

**Guessing at what counts as "stale."** No threshold is built in here — confirm the actual set of indexes with the user before touching anything.

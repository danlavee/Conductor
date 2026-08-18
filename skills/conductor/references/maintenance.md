# Maintenance

Throughout this document, `<agent>` is always literally `conductor-maintenance` — never any other name.

On the first join under this name only, carry the responsibility string:

```text
conductor <agent> join "repo upkeep: archive stale Conductor history, keep collaboration/rules seeded, never delete data"
```

Every later join must omit it — `conductor <agent> join` — because the registry rejects a responsibility argument for a name already on the roster. The wording is a team convention, not a rule the registry enforces: a paraphrase on the first join is accepted, and is then what the roster says forever.

Archiving and pruning set no automatic policy, no staleness threshold, and no schedule — ask the user which action to take. Skill currency is different: it has a real owner and a real trigger, below.

## Procedure: checking skill currency

Run this at the start of every session, before anything else. It is the one duty here with a standing trigger, precisely because nothing else in the system checks it and an out-of-date skill can sit unnoticed indefinitely.

1. Determine the current source the same way [update-skill.md](update-skill.md) step 1 does (defaults to `go install github.com/danlavee/Conductor/cmd/conductor@latest`; compile first if using a local checkout).
2. Run `conductor verify <installed-skill-directory>` using that binary. It is read-only and writes nothing.
3. `current` — note it and move on to whatever else you were asked to do.
4. `outdated` — follow [update-skill.md](update-skill.md)'s full procedure.
5. `unknown` (no manifest, can't tell) or `not-installed` — this copy was never placed via `conductor install`; report that finding rather than guessing.

## Procedure: archiving and pruning history

When a user asks to prune, archive, clean up, or redact old entries from a topic's history:

1. **Read the live history.** `conductor <agent> get <group>/<topic> --full` fetches the first capped page; follow `remaining` with indexed range reads until the complete decision range has been inspected.
2. **Confirm the targets with the user.** Identify the target record set, and check each record's text. If any record to be pruned does *not* contain strikethrough markdown (`~~`), warn the user explicitly:
   > *"Warning: Record #12 'task text' is not struck through (active). Are you sure you want to permanently prune it?"*

   Have the user confirm the final deletion list before execution. Never apply automatic thresholds or guess.
3. **Commit the archive sibling.** Copy the target records into a sibling `-archive` topic before pruning the live file. Write them to a file first, one JSON string per line, and load that — a quoted argument would publish one prose record instead of a copy, and pruning after it destroys the originals with nothing faithful to restore from:
   ```bash
   conductor <agent> put <group/topic>-archive --jsonl=<absolute-path-to-jsonl>
   ```
4. **Prune the live state.** Only after the archive copy is confirmed committed:
   ```bash
   conductor <agent> redact <group/topic> 57
   conductor <agent> redact <group/topic> --start=1 --end=50
   ```
   To mark an archived record in place instead of removing it, `conductor <agent> strike <group/topic> <index>`, one index at a time.
5. **Verify.** `conductor <agent> get <group/topic> --full`. If it reports `remaining`, continue through indexed range reads across the affected indexes before declaring success, then tell the user about the backup on disk.

## Where this goes wrong

**Striking before the archive commit is confirmed.** A crash between the two must never leave a record struck in the original topic without a confirmed copy in the archive — archive first, verify, strike second, never the reverse.

**Accidental deletion.** Physical redaction is destructive and permanent. Conductor writes a timestamped, nanosecond-precise backup (`history.jsonl.bak-*`) in the topic folder before writing physical changes.

**Index shifting.** Redacting leaves a stable chronological hole. Later indexes never shift, so references like `#57` stay valid.

**Redaction wakes the team.** A redaction publishes an ordinary `update` signal for the topic — to its subscribers, and to every registered agent if it is a `collaboration` broadcast topic. Recipients cannot tell it from a publication. Warn the user before pruning a busy topic.

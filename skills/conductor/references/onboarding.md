# Onboarding

This is the first live join after the skill itself is already installed. It runs once per state root — it seeds `collaboration/rules`, which every future agent receives automatically through the forced broadcast, so it must not run twice against a root that already has rules on it.

Throughout this document, `<agent>` is always literally `conductor-maintenance` — never any other name.

1. **Check whether this has already happened.** `conductor <agent> list --topic-group=collaboration`, which needs no registration. If it names `collaboration/agents`, this state root has had agents and has been onboarded before. Stop this flow and join as an ordinary agent per the main workflow instead. An empty list, `[]`, is the answer you are looking for: nobody has joined here.
2. **Explain the model before touching the bus.** In a few sentences, not a lecture: independent agents, each keeping their own context, coordinating through a shared bus instead of a merged conversation. Enough for the user to understand what's about to start, not a full tour — the rest of this document and the linked references cover the mechanics if asked.
3. **Join as `conductor-maintenance`** with exactly this responsibility string, unchanged, on initial join: `conductor <agent> join "repo upkeep: keep the installed Conductor skill current, keep collaboration/rules seeded, never delete data"`. Subsequent re-joins must omit the responsibility string.
4. **Seed the rules.** `conductor <agent> get collaboration/rules --full`. If it comes back empty, resolve `assets/collaboration-rules.jsonl` relative to this skill's own directory and run `conductor <agent> put collaboration/rules --jsonl=<that absolute path>`. If it comes back non-empty, stop — do not overwrite or add to existing rules; tell the user rules are already seeded and ask before doing anything further.
5. **Hand off.** Tell the user onboarding is done: `conductor-maintenance` has joined, and every agent that joins from here on receives the seeded rules automatically, without needing to read this file or the rules file themselves. Continue as an ordinary agent per the rest of `SKILL.md`.

## Where this goes wrong

**Re-running this on a live root.** Steps 1 and 4 both guard against it; neither is optional. `put` always creates new records, so a second seed pass duplicates every rule rather than erroring on its own.

**Skipping the explanation in step 2.** Onboarding exists so the user understands the model before independent agents start publishing to a shared bus on their behalf, not just to get `conductor-maintenance` registered.

**Joining `conductor-maintenance` with paraphrased responsibility text.** The registry does not police the wording — it accepts whatever the initial join carries, and that is then what the roster says forever. Getting it right is on you, not on a check. On every later re-join the responsibility must be omitted entirely, or the registry rejects it.

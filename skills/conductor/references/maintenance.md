# Maintenance

Throughout this document, `<agent>` is always literally `conductor-maintenance` — never any other name.

On the first join under this name only, carry the responsibility string:

```text
conductor <agent> join "repo upkeep: keep the installed Conductor skill current, keep collaboration/rules seeded, never delete data"
```

Every later join must omit it — `conductor <agent> join` — because the registry rejects a responsibility argument for a name already on the roster. The wording is a team convention, not a rule the registry enforces: a paraphrase on the first join is accepted, and is then what the roster says forever.

## Procedure: checking skill currency

Run this at the start of every session, before anything else. It is the one duty here with a standing trigger, precisely because nothing else in the system checks it and an out-of-date skill can sit unnoticed indefinitely.

1. Determine the current source the same way [update-skill.md](update-skill.md) step 1 does (defaults to `go install github.com/danlavee/Conductor/cmd/conductor@latest`; compile first if using a local checkout).
2. Run `conductor verify <installed-skill-directory>` using that binary. It is read-only and writes nothing.
3. `current` — note it and move on to whatever else you were asked to do.
4. `outdated` — follow [update-skill.md](update-skill.md)'s full procedure.
5. `unknown` — no manifest, or the content changed since install; follow [update-skill.md](update-skill.md)'s `unknown` step. `not-installed` — nothing is at that directory; report that finding rather than guessing.

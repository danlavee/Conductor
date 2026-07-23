# Updating the skill

This is a conversation you carry out, not a CLI command. When asked to update or redeploy the skill:

1. **Determine the source.** Defaults to the latest on GitHub — `go install github.com/danlavee/Conductor/cmd/conductor@latest` is the natural fetch, since that's the module path. If told to use a local checkout, always compile the codebase first (`go build ./cmd/conductor`) to ensure you do not use an outdated pre-compiled binary.
2. **Check whether the installed skill has been locally edited.** Compare the installed directory's current file hashes against its own install manifest. A mismatch means someone changed something since install — by hand or by another agent.
   - Edits found: diff current content against what the manifest recorded, show the difference, stop. Get explicit direction — overwrite, abort, or reconcile manually — before touching anything.
   - Clean: continue.
3. **Diff installed vs. incoming.** Show what the update would actually change — skill content, binary version, protocol — before doing anything.
4. **Check protocol compatibility.** Compare the incoming binary's protocol against the live state root's declared version.
5. **Get explicit approval** before anything destructive.
6. **Execute.** Announce on the bus, check for other running processes, migrate if step 4 required it, create a pre-upgrade backup snapshot of the active binary (by appending a `.pre-upgrade-[timestamp]` suffix to its filename), rename-then-place swap, verify with real functional calls — not just `version`.
7. **Sync** the repo and installed skill copies afterward.

## Where this goes wrong

**Migrate.** A protocol mismatch is never a direct swap — always `migrate` into a fresh destination first; the source root is read-only and stays untouched regardless of outcome. If migration reports anything unexpected (counts that don't match your own read of the source, a refusal over an uncommitted transaction), stop and surface it instead of forcing the swap through. Confirm the destination actually lands at the current protocol, not an intermediate hop — a v1 root migrates straight through to the latest version, not just to v2.

**Diffs.** Steps 2 and 3 are two different comparisons and both matter: installed-vs-manifest tells you if there are local edits to protect; installed-vs-incoming tells you what's about to change. Showing only one gives a false picture — if both are true at once (local edits exist *and* the incoming version differs from what was originally installed), the user needs to see both before deciding, not a collapsed summary of one.

**Conflicts.** When a local edit and an incoming change touch the same content, there's no merge algorithm here — the installed copy isn't a git checkout. Present both versions plainly and let the decision be made explicitly; never silently prefer one side because it seems more likely to be "right."

**Conversational flexibility.** This procedure has no rigid syntax to enforce — skip a step if told to, accept "just use this folder" without demanding a formal parameter, answer questions mid-flow. The one place that doesn't flex is step 5: get approval before the destructive step even if asked to move fast. Skipping exactly that is what caused a real outage once already.

# Updating the skill

This is a conversation you carry out, not a CLI command. When asked to update or redeploy the skill:

1. **Determine the source.** Defaults to the latest on GitHub — `go install github.com/danlavee/Conductor/cmd/conductor@latest` is the natural fetch, since that's the module path. If told to use a local checkout, always compile the codebase first (`go build ./cmd/conductor`) to ensure you do not use an outdated pre-compiled binary.
2. **Check currency.** Run `conductor verify <installed-skill-directory>` using the binary from step 1 — read-only, writes nothing.
   - `current`: nothing to do. Stop here.
   - `not-installed`: this isn't an update, it's a fresh install — use `conductor install` directly instead of this procedure.
   - `unknown`: no manifest, or the installed content no longer matches the one it has — someone changed something since install, by hand or by another agent. Diff current content against whatever reference is available, show the difference, stop. Get explicit direction — overwrite, abort, or reconcile manually — before touching anything.
   - `outdated`: continue — show what would actually change (skill content, binary version, protocol) before doing anything. `verify` only reports *that* it differs, not *what*; that diff is still yours to produce.
3. **Check protocol compatibility.** Compare the incoming binary's protocol against the live state root's declared version.
4. **Get explicit approval** before anything destructive.
5. **Execute.** Announce on the bus, then use one explicit cutover identity:
   1. Confirm the incoming `version` reports `cutover_capability: 1`. Finish or abort every durable transaction.
   2. Run `conductor cutover freeze <absolute-active-root> --id=<id> --release=<release>`. This closes operation admission, drains in-flight calls, refuses active legacy watchers, and leaves writes blocked.
   3. If the protocol changes, run `conductor migrate <absolute-active-root> <absolute-staging-root>` and validate the staging root with real reads. Keep the old root as the rollback snapshot.
   4. Replace the root generation at the same logical path, binary, and skill while still frozen. Then run `conductor cutover replace <absolute-active-root> --id=<id>`.
   Update the host adapter's tree in the same step. It carries its own copy of the executable, and an adapter left behind runs the outgoing CLI against the incoming generation — the hooks keep firing, so nothing reports an error.
   5. Let every old watcher fire its one no-delta `conductor-replaced` activation and exit. Reload the installed skill and binary; each host's adapter restores its own delivery stream against the new generation.
   6. Run `conductor cutover activate <absolute-active-root> --id=<id>`, then verify one fresh publication, delivery, and acknowledgment.
   Before the root is replaced, `conductor cutover abort <absolute-active-root> --id=<id>` safely returns to active admission. After replacement, never abort; resume the same `replace`/`activate` cutover identity. A timeout never unfreezes the system.
6. **Sync** the repo and every installed copy afterward — the skill tree and each installed adapter tree.

## Where this goes wrong

**Migrate.** A protocol mismatch is never a direct swap — always `migrate` into a fresh destination first; the source root is read-only and stays untouched regardless of outcome. If migration reports anything unexpected (counts that don't match your own read of the source, a refusal over an uncommitted transaction), stop and surface it instead of forcing the swap through. Confirm the destination actually lands at the current protocol, not an intermediate hop — a v1 root migrates straight through to the latest version, not just to v2.

**Diffs.** `verify`'s status and a content diff are two different comparisons and both matter: `unknown` tells you if there are local edits to protect; `outdated` tells you a real update exists but not what's in it. Don't collapse them — if both are true at once (local edits exist *and* the incoming version differs from what was originally installed, which `verify` alone can't distinguish since either produces a mismatched manifest), the user needs to see both before deciding.

**Conflicts.** When a local edit and an incoming change touch the same content, there's no merge algorithm here — the installed copy isn't a git checkout. Present both versions plainly and let the decision be made explicitly; never silently prefer one side because it seems more likely to be "right."

**Conversational flexibility.** This procedure has no rigid syntax to enforce — skip a step if told to, accept "just use this folder" without demanding a formal parameter, answer questions mid-flow. The one place that doesn't flex is step 4: get approval before the destructive step even if asked to move fast. Skipping exactly that is what caused a real outage once already.

**Environment variances.** Avoid shell auto-formatters that truncate outputs, prefer native platform-neutral scripting properties, and verify exact CLI subcommand syntax before guessing flags.

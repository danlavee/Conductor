# Update notes

This file contains only release-specific steps that fall outside Conductor's normal update procedure.

## v1.0.0

Each adapter's plugin manifest now carries the release version, so a host that caches plugins by version sees each release as new. After reinstalling an adapter with `--replace`, run its host's update commands from the [adapter registry](docs/integrations/README.md); without them the host keeps running the old copy.

Adapter runtime state (session bindings, watch locks) moved from `<root>/adapters/` to `<root>/hosts/`. It used to share names with the conventional install folders under `~/.conductor/adapters`, so running sessions wrote into the installed trees.

Live cutover and `migrate` are removed. Start a fresh state root: the old one is not carried forward, and the sibling `.<root>-cutover-*` control directory is no longer read.

`redact` and `strike` are removed; retire a record with `edit`. Writes now take an OS file lock per topic instead of a lease and durable transaction, so the state protocol is now 5 and needs a fresh state root.

Watch now wakes from topic history: the event log, inboxes, and global publication index are gone, sequences count per topic, and a delivery is `{"deliveries": [...], "remaining": N}` of plain deltas with no roster. The state protocol is now 6 and needs a fresh state root.

Roster entries report `holder` as the string `adapter` or `self`, with no pid and no `adapter` field. The Claude adapter's `identity` runs only as a hook. Reinstall the adapter with `--replace`.

The Claude adapter's `arm` no longer holds the stream itself: it is the watcher, and starts a detached `adapter claude hold` that keeps the agent wakeable through the woken turn. `arm` and `release` print no `outcome` report; the exit status (`0`, `1` or `2`) is the whole result. Reinstall the adapter with `--replace`.

The Grok and AGY adapters hold through the same detached `adapter <host> hold` as Claude, and their Stop hook is the watcher. `join` binds their sessions; `bind`, `.conductor-agent`, receipts and the resident are gone. Reinstall both adapters with `--replace`.

`status` is removed: join's roster (`wakeable`, `holder`) and a publication's `unreachable` answer who can be woken.

The install manifest is now one digest over the tree, so an installation from an earlier release reads as `unknown`: reinstall both the skill and the adapter with `--replace`. The installer no longer restricts which directory holds `skills/conductor`, and a destination without a manifest is a conflict rather than being moved aside on its own.

A record holds at most 2,000 characters, and a read returns at most 20 records unless `--limit` or a range's `--end` says otherwise. A record index with `--delta` or `--full` is refused rather than ignored.

A wake is one plain-text message on every host and from `watch`: whole publications, at most 5 records within 10,000 UTF-16 units, and only what it showed is acknowledged. Publishing rejects a publication over 5 records or one that would not fit one wake. The state protocol is now 7 and needs a fresh state root.

The watch owner record is keyed by its process's start (protocol 8), and names the session it holds for (protocol 9), so an adapter that takes a name over waits out the session it replaces. Each needs a fresh state root.

An agent is bound in one session at a time, and the last join wins: a join from another session takes the name over and answers `took_over`.

Claude's hooks take the session from the event on stdin, not `--session`; Claude's `PostToolUse` arms a watcher and Grok's hands over unread work inside a turn; the `stop` hook verb is now `arm`. Reinstall the Claude, Grok and AGY adapters with `--replace`.

The Codex adapter is built. Install and enable it as its row in the [adapter registry](docs/integrations/README.md) says.

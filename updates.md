# Update notes

This file contains only release-specific steps that fall outside Conductor's normal update procedure.

## Unreleased

Live cutover and `migrate` are removed. Start a fresh state root: the old one is not carried forward, and the sibling `.<root>-cutover-*` control directory is no longer read. The Claude adapter's `replaced` outcome is gone, so the report codes after it moved down by one: `refused` is now 12 through `orphaned` at 16.

`redact` and `strike` are removed; retire a record with `edit`. Writes now take an OS file lock per topic instead of a lease and durable transaction, so the state protocol is now 5 and needs a fresh state root.

The install manifest is now one digest over the tree, so an installation from an earlier release reads as `unknown`: reinstall both the skill and the adapter with `--replace`. The installer no longer restricts which directory holds `skills/conductor`, and a destination without a manifest is a conflict rather than being moved aside on its own.

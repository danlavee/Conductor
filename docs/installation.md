# Install Conductor

Two installations, and both matter. The skill teaches an agent how to collaborate; the host adapter is what makes work addressed to that agent start a turn. Installing only the skill produces an agent that is reachable — nothing published to it is lost — but not wakeable, so it reads its mail only when a human happens to prompt it. That is the failure this project exists to remove, so treat the adapter as part of installing, not as an optional extra.

## Guidance

Install the skill and its matching native CLI together. The installer requires Go 1.22 or newer.

Before installing, ask: "Install for this project or for the user across projects?" Do not infer the scope.

## Steps

1. Resolve and show the absolute destination, using the installing host's own vendor-specific skills directory if it has one, or the generic `.agents` convention otherwise:
   - Project: `<project>/<vendor-dir>/skills/conductor`
   - User: `$HOME/<vendor-dir>/skills/conductor`
2. Confirm that its parent is the intended skills directory. For a project install, disclose that this changes the project tree.
3. Wait for confirmation, then run:

```bash
go run github.com/danlavee/Conductor/cmd/conductor@latest install "<absolute-skill-directory>"
```

## Then install the host's adapter

Check the [adapter registry](integrations/README.md) for the installing host. A host with no adapter yet keeps the interim fallback described there, and the steps below do not apply to it.

For Claude Code:

1. Resolve and show the absolute destination, ending in `adapters/claude-code`. The outer directory is yours to choose — `$HOME/.conductor/adapters/claude-code` is the conventional one — because the host is pointed at the result rather than discovering it.
2. Wait for confirmation, then run:

```bash
go run github.com/danlavee/Conductor/cmd/conductor@latest adapter claude install "<absolute-adapter-directory>"
```

That places the plugin and the executable its hooks name, together, so the two cannot disagree about where the binary is. One step remains and it is the user's: enable the plugin with the host's own commands, `claude plugin marketplace add <the directory above>`, then install `conductor@conductor-adapters`. Conductor does not write the host's plugin registry — see [the adapter's README](../adapters/claude-code/README.md) for why. Nothing is configured per project: in a session the plugin serves, `join` answers `"wake": "adapter"` and binds the session to the joining name.

Confirm the result in a new session: `join` answers `"wake": "adapter"`, and after the turn ends `conductor <agent> status` reports `"holder": "adapter"`. A join answering `"watch"` means the session was not served — the plugin is missing, not enabled, or was enabled after the session started.

## Behavior rules

Use the confirmed absolute destination as the command's only argument. Do not infer another destination, use a relative path, or replace a different existing installation.

The installer stages the tree beside the destination, runs the staged binary's `version` to prove it starts and matches, and only then renames it into place. The installed `.conductor-manifest.json` records the release, the state protocol, and one digest over every file, the binary included. Re-running the same distribution is idempotent. Anything else at the destination — another release, a tree edited since install, or one without a manifest — is a conflict, and the error names both versions: installing never replaces. Replacing an existing installation is a separate operation, `--replace`, run with every agent that uses the installation stopped — described in the skill's [update procedure](../skills/conductor/references/update-skill.md).

`conductor version` reports the software release and numeric state protocol without opening or creating shared state.

## Interactivity rules

Do not install until the user has chosen the scope and confirmed the exact destination. Stop and report any conflict instead of choosing or repairing a location yourself.

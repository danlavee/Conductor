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

That places the plugin and the executable its hooks name, together, so the two cannot disagree about where the binary is. Two steps remain and both are the user's:

- Enable the plugin with the host's own commands: `claude plugin marketplace add <the directory above>`, then install `conductor@conductor-adapters`. Conductor does not write the host's plugin registry — see [the adapter's README](../adapters/claude-code/README.md) for why.
- Bind each project to an identity by writing its agent name into a `.conductor-agent` file at the project root. Plain UTF-8; a byte order mark is tolerated. A project without one loads the adapter and does nothing, which is correct for a project that has not opted in.

Two agents working in the same directory need one more step, because the project file names one identity for every session that opens there. The second agent binds its own session from inside it, with `conductor adapter claude bind <agent>` — see [the adapter's page](integrations/claude-code.md) for how the two bindings rank. Without it both sessions resolve to the same identity, contend for its one stream, and the loser is refused at every turn end while looking exactly like a working install.

Confirm the result with `conductor <agent> status`, which reports `wakeable` and the holding process. `registered: true, wakeable: false` means the identity joined but nothing is holding a stream for it — the adapter is missing, not enabled, or the project is unbound.

## Behavior rules

Use the confirmed absolute destination as the command's only argument. Do not infer another destination, use a relative path, or replace a different existing installation.

The installed manifest binds the skill, native binary, software release, platform, and supported state protocol. Re-running the same distribution is idempotent. A different manifest format, protocol, or payload is a conflict; the installer does not replace or migrate it in place.

`conductor version` reports the software release and numeric state protocol without opening or creating shared state.

## Interactivity rules

Do not install until the user has chosen the scope and confirmed the exact destination. Stop and report any conflict instead of choosing or repairing a location yourself.
